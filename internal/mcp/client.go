package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPClient manages a single MCP server connection.
type MCPClient struct {
	name          string
	client        *sdkmcp.Client
	session       *sdkmcp.ClientSession
	processCancel context.CancelFunc
}

// Connect establishes a connection to an MCP server using the specified transport.
func Connect(ctx context.Context, name, command string, args []string, env []string, transport, url string) (*MCPClient, error) {
	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "local-agent", Version: "0.2.0"},
		nil,
	)

	var t sdkmcp.Transport
	var processCancel context.CancelFunc
	var stopConnectCancellation func() bool

	switch transport {
	case "sse":
		if url == "" {
			return nil, fmt.Errorf("sse transport requires url for %s", name)
		}
		t = &sdkmcp.SSEClientTransport{Endpoint: url}
	case "streamable-http":
		if url == "" {
			return nil, fmt.Errorf("streamable-http transport requires url for %s", name)
		}
		t = &sdkmcp.StreamableClientTransport{Endpoint: url}
	default: // "stdio" or ""
		if command == "" {
			return nil, fmt.Errorf("stdio transport requires command for %s", name)
		}
		resolvedCommand, err := resolveExecutable(command)
		if err != nil {
			return nil, fmt.Errorf("stdio transport command for %s: %w", name, err)
		}
		// The SDK owns cmd.Wait, while MCPClient owns cancellation of the
		// process tree. Use a detached process context so the short connection
		// timeout can be unhooked after successful initialization.
		processCtx, cancelProcess := context.WithCancel(context.Background())
		processCancel = cancelProcess
		stopConnectCancellation = context.AfterFunc(ctx, cancelProcess)
		cmd := exec.CommandContext(processCtx, resolvedCommand, args...)
		configureProcessCancellation(cmd)
		cmd.Env = childEnvironment(cmd.Environ(), env)
		t = &sdkmcp.CommandTransport{Command: cmd}
	}

	session, err := client.Connect(ctx, t, nil)
	if stopConnectCancellation != nil {
		stopConnectCancellation()
	}
	if err != nil {
		if processCancel != nil {
			processCancel()
		}
		return nil, fmt.Errorf("connect to %s: %w", name, err)
	}

	return &MCPClient{
		name:          name,
		client:        client,
		session:       session,
		processCancel: processCancel,
	}, nil
}

// Name returns the server name.
func (c *MCPClient) Name() string { return c.name }

// ListTools returns all tools from this server.
func (c *MCPClient) ListTools(ctx context.Context) ([]*sdkmcp.Tool, error) {
	caps := c.session.InitializeResult()
	if caps == nil || caps.Capabilities.Tools == nil {
		return nil, nil
	}

	var tools []*sdkmcp.Tool
	for tool, err := range c.session.Tools(ctx, nil) {
		if err != nil {
			return tools, fmt.Errorf("list tools from %s: %w", c.name, err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// CallTool invokes a tool on this server.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	result, err := c.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("call tool %s on %s: %w", name, c.name, err)
	}

	return &ToolResult{
		Content: renderToolResult(result),
		IsError: result.IsError,
	}, nil
}

const maxRenderedMCPResultBytes = 96 * 1024

// renderToolResult preserves structured MCP data and emits bounded receipts
// for binary/resource blocks. Passing base64 media directly to a small model
// would waste its context and previously those blocks disappeared entirely.
func renderToolResult(result *sdkmcp.CallToolResult) string {
	const truncatedMarker = "\n... [MCP result truncated]"
	var b strings.Builder
	truncated := false
	appendPart := func(part string) {
		if truncated || part == "" {
			return
		}
		if b.Len() > 0 {
			part = "\n" + part
		}
		remaining := maxRenderedMCPResultBytes - len(truncatedMarker) - b.Len()
		if remaining <= 0 {
			truncated = true
			return
		}
		if len(part) > remaining {
			b.WriteString(part[:remaining])
			truncated = true
			return
		}
		b.WriteString(part)
	}

	for _, content := range result.Content {
		switch value := content.(type) {
		case *sdkmcp.TextContent:
			appendPart(value.Text)
		case *sdkmcp.ImageContent:
			appendPart(fmt.Sprintf("[image: mime=%s encoded_bytes=%d]", value.MIMEType, len(value.Data)))
		case *sdkmcp.AudioContent:
			appendPart(fmt.Sprintf("[audio: mime=%s encoded_bytes=%d]", value.MIMEType, len(value.Data)))
		case *sdkmcp.ResourceLink:
			appendPart(fmt.Sprintf("[resource: uri=%s name=%s mime=%s]", value.URI, value.Name, value.MIMEType))
		case *sdkmcp.EmbeddedResource:
			if value.Resource == nil {
				appendPart("[embedded resource: empty]")
			} else if value.Resource.Text != "" {
				appendPart(fmt.Sprintf("[embedded resource: uri=%s mime=%s]\n%s", value.Resource.URI, value.Resource.MIMEType, value.Resource.Text))
			} else {
				appendPart(fmt.Sprintf("[embedded resource: uri=%s mime=%s encoded_bytes=%d]", value.Resource.URI, value.Resource.MIMEType, len(value.Resource.Blob)))
			}
		default:
			appendPart(fmt.Sprintf("[MCP content: %T]", content))
		}
	}

	if result.StructuredContent != nil {
		if structured, err := json.Marshal(result.StructuredContent); err == nil {
			appendPart("structured: " + string(structured))
		} else {
			appendPart(fmt.Sprintf("[structured content could not be encoded: %v]", err))
		}
	}
	if truncated {
		b.WriteString(truncatedMarker)
	}
	return b.String()
}

// Close shuts down the MCP server connection.
func (c *MCPClient) Close() error {
	if c.processCancel != nil {
		c.processCancel()
	}
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// IsConnected returns true if the client has an active session.
func (c *MCPClient) IsConnected() bool {
	return c.session != nil
}

// Ping checks if the server is still responsive.
// Returns nil if the server responds, error otherwise.
func (c *MCPClient) Ping(ctx context.Context) error {
	if c.session == nil {
		return fmt.Errorf("no session")
	}

	// Try listing tools as a health check
	_, err := c.ListTools(ctx)
	return err
}
