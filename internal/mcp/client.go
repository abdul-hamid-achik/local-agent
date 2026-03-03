package mcp

import (
	"context"
	"fmt"
	"os/exec"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPClient manages a single MCP server connection.
type MCPClient struct {
	name    string
	client  *sdkmcp.Client
	session *sdkmcp.ClientSession
	cmd     *exec.Cmd
}

// Connect establishes a connection to an MCP server using the specified transport.
func Connect(ctx context.Context, name, command string, args []string, env []string, transport, url string) (*MCPClient, error) {
	client := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "local-agent", Version: "0.2.0"},
		nil,
	)

	var t sdkmcp.Transport

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
		cmd := exec.Command(command, args...)
		if len(env) > 0 {
			cmd.Env = append(cmd.Environ(), env...)
		}
		t = &sdkmcp.CommandTransport{Command: cmd}
	}

	session, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", name, err)
	}

	return &MCPClient{
		name:    name,
		client:  client,
		session: session,
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

	// Extract text content from the result.
	var text string
	for _, ct := range result.Content {
		if tc, ok := ct.(*sdkmcp.TextContent); ok {
			if text != "" {
				text += "\n"
			}
			text += tc.Text
		}
	}

	return &ToolResult{
		Content: text,
		IsError: result.IsError,
	}, nil
}

// Close shuts down the MCP server connection.
func (c *MCPClient) Close() error {
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}
