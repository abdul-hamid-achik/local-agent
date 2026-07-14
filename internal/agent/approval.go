package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	executionpkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	permissionpkg "github.com/abdul-hamid-achik/local-agent/internal/permission"
)

const (
	maxApprovalDiffInputBytes = 1024 * 1024
	maxApprovalDiffCells      = 2_000_000
	maxApprovalDiffBytes      = 256 * 1024
)

func (a *Agent) newApprovalRequest(ctx context.Context, tc llm.ToolCall, argumentsHash string) permissionpkg.ApprovalRequest {
	workspace := a.filesystemContext().workDir
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	if absolute, err := filepath.Abs(workspace); err == nil {
		workspace = filepath.Clean(absolute)
	}
	request := permissionpkg.ApprovalRequest{
		RequestID:       tc.ID,
		ToolName:        tc.Name,
		Args:            cloneApprovalArguments(tc.Arguments),
		ArgumentsSHA256: argumentsHash,
		Scope: permissionpkg.ApprovalScope{
			Kind:      permissionpkg.ScopeExactRequest,
			Workspace: workspace,
			Resource:  argumentsHash,
		},
	}
	request.Preview = a.buildApprovalPreview(ctx, tc, argumentsHash)
	return request
}

func cloneApprovalArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = cloneApprovalValue(value)
	}
	return cloned
}

func cloneApprovalValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneApprovalArguments(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i := range typed {
			cloned[i] = cloneApprovalValue(typed[i])
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func (a *Agent) buildApprovalPreview(ctx context.Context, tc llm.ToolCall, argumentsHash string) permissionpkg.ApprovalPreview {
	preview := permissionpkg.ApprovalPreview{
		Kind:            permissionpkg.PreviewGeneric,
		ArgumentsSHA256: argumentsHash,
	}
	if def, ok := a.mcpToolDefinition(tc.Name); ok {
		preview.ActionLabel = boundApprovalLabel(def.DisplayName)
		preview.Consequence = mcpApprovalConsequence(def.Behavior)
	}
	pathArg := func(key string, destructive bool) string {
		raw, _ := tc.Arguments[key].(string)
		if raw == "" {
			return ""
		}
		var (
			resolved string
			err      error
		)
		if destructive {
			resolved, err = a.resolveDestructivePath(raw)
		} else {
			resolved, err = a.resolvePath(raw)
		}
		if err == nil {
			return resolved
		}
		return raw
	}
	readablePathArg := func(key string) string {
		raw, _ := tc.Arguments[key].(string)
		if raw == "" {
			return ""
		}
		resolved, err := a.resolveReadablePath(raw)
		if err == nil {
			defer func() { _ = resolved.close() }()
			return resolved.absolute
		}
		return raw
	}

	switch tc.Name {
	case "write":
		preview.Kind = permissionpkg.PreviewFileWrite
		preview.Path = pathArg("path", false)
		content, _ := tc.Arguments["content"].(string)
		preview.ByteSize = int64(len(content))
		preview.ContentSHA256 = executionpkg.HashText(content)
		before, exists, reason := a.approvalExistingContent(preview.Path)
		if exists {
			preview.ExistingSHA256 = executionpkg.HashText(before)
		}
		if reason != "" {
			preview.DiffOmittedReason = reason
			break
		}
		preview.Diff, preview.DiffTruncated, preview.DiffOmittedReason = approvalDiff(ctx, before, content)
	case "edit":
		preview.Kind = permissionpkg.PreviewFilePatch
		preview.Path = pathArg("path", false)
		patch, _ := tc.Arguments["patch"].(string)
		preview.Diff = patch
		if len(preview.Diff) > maxApprovalDiffBytes {
			preview.Diff = truncateApprovalUTF8(preview.Diff, maxApprovalDiffBytes)
			preview.DiffTruncated = true
		}
		before, exists, reason := a.approvalExistingContent(preview.Path)
		if exists {
			preview.ExistingSHA256 = executionpkg.HashText(before)
		}
		if reason == "" && exists {
			if after, err := applyPatch(before, patch); err == nil {
				preview.ByteSize = int64(len(after))
				preview.ContentSHA256 = executionpkg.HashText(after)
			} else {
				preview.DiffOmittedReason = fmt.Sprintf("patch could not be applied for preview: %v", err)
			}
		} else if reason != "" {
			preview.DiffOmittedReason = reason
		}
	case "bash":
		preview.Kind = permissionpkg.PreviewCommand
		preview.Command, _ = tc.Arguments["command"].(string)
	case "copy":
		preview.Kind = permissionpkg.PreviewFilesystem
		preview.SourcePath = readablePathArg("source")
		preview.DestinationPath = pathArg("destination", false)
	case "move":
		preview.Kind = permissionpkg.PreviewFilesystem
		preview.SourcePath = pathArg("source", true)
		preview.DestinationPath = pathArg("destination", true)
	case "remove":
		preview.Kind = permissionpkg.PreviewFilesystem
		preview.Path = pathArg("path", true)
	case "mkdir":
		preview.Kind = permissionpkg.PreviewFilesystem
		preview.Path = pathArg("path", false)
	default:
		for _, key := range []string{"path", "file_path", "filename", "file"} {
			if raw, ok := tc.Arguments[key].(string); ok && raw != "" {
				preview.Path = raw
				break
			}
		}
	}
	return preview
}

// revalidateApprovalPreview verifies the exact presentation contract the user
// approved. It never substitutes a newly built preview: any changed path,
// existence/content hash, diff, command, or bounded metadata fails closed and
// requires a fresh modal.
func (a *Agent) revalidateApprovalPreview(ctx context.Context, tc llm.ToolCall, request permissionpkg.ApprovalRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current := a.buildApprovalPreview(ctx, tc, request.ArgumentsSHA256)
	if err := ctx.Err(); err != nil {
		return err
	}
	if current != request.Preview {
		return errors.New("approval preview changed while the request was open")
	}
	return nil
}

func mcpApprovalConsequence(behavior llm.ToolBehavior) string {
	if !behavior.Declared {
		return "The MCP server supplied no effect metadata. This call may read data, contact external systems, or change durable state; inspect the exact arguments."
	}
	// MCP destructiveHint is meaningful only for non-read-only tools. Keep the
	// call effect-unknown and approval-gated, but do not present a conventional
	// readOnlyHint as an asserted destructive operation.
	if behavior.ReadOnly {
		if behavior.OpenWorld {
			return "The server declares this read-only, but it may contact external systems. Server annotations are untrusted."
		}
		return "The server declares this read-only. Server annotations are untrusted, so the call remains effect-unknown and approval-gated."
	}
	consequence := "Server metadata indicates this call may create or update durable state."
	if behavior.Destructive {
		consequence = "Server metadata indicates this call may make destructive changes."
	}
	if behavior.OpenWorld {
		consequence = strings.TrimSuffix(consequence, ".") + " and contact external systems."
	}
	return consequence
}

func boundApprovalLabel(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	const maximumBytes = 160
	if len(value) <= maximumBytes {
		return value
	}
	return truncateApprovalUTF8(value, maximumBytes-3) + "..."
}

func (a *Agent) approvalExistingContent(path string) (content string, exists bool, omittedReason string) {
	if strings.TrimSpace(path) == "" {
		return "", false, "target path is unavailable"
	}
	workspace, err := a.openWorkspaceRoot()
	if err != nil {
		return "", false, fmt.Sprintf("existing content unavailable: %v", err)
	}
	defer func() { _ = workspace.Close() }()
	_, relative, err := workspace.resolve(a, path, false)
	if err != nil {
		return "", false, fmt.Sprintf("existing content unavailable: %v", err)
	}
	parent, name, err := workspace.openParent(relative, false)
	if os.IsNotExist(err) {
		return "", false, ""
	}
	if err != nil {
		return "", false, fmt.Sprintf("existing content unavailable: %v", err)
	}
	defer func() { _ = parent.Close() }()
	data, _, err := readPinnedRootFile(parent, name, maxApprovalDiffInputBytes)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, ""
	}
	if err != nil {
		return "", false, fmt.Sprintf("existing content unavailable: %v", err)
	}
	return string(data), true, ""
}

func approvalDiff(ctx context.Context, before, after string) (diff string, truncated bool, omittedReason string) {
	if before == after {
		return "", false, ""
	}
	if len(before) > maxApprovalDiffInputBytes || len(after) > maxApprovalDiffInputBytes {
		return "", false, fmt.Sprintf("diff omitted: input exceeds %d bytes; exact content remains bound by SHA-256", maxApprovalDiffInputBytes)
	}
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")
	if len(beforeLines) > 0 && len(afterLines) > maxApprovalDiffCells/len(beforeLines) {
		return "", false, fmt.Sprintf("diff omitted: %d x %d lines; exact content remains bound by SHA-256", len(beforeLines), len(afterLines))
	}
	diff, err := computeDiff(ctx, beforeLines, afterLines)
	if err != nil {
		return "", false, fmt.Sprintf("diff unavailable: %v", err)
	}
	if len(diff) <= maxApprovalDiffBytes {
		return diff, false, ""
	}
	return truncateApprovalUTF8(diff, maxApprovalDiffBytes), true, ""
}

func truncateApprovalUTF8(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func approvalGrantKey(request permissionpkg.ApprovalRequest) string {
	return request.Scope.Workspace + "\x00" + request.ToolName + "\x00" + request.Scope.Kind + "\x00" + request.Scope.Resource
}

func (a *Agent) hasSessionApproval(request permissionpkg.ApprovalRequest) bool {
	key := approvalGrantKey(request)
	a.mu.RLock()
	_, ok := a.approvalGrants[key]
	a.mu.RUnlock()
	return ok
}

func (a *Agent) rememberSessionApproval(request permissionpkg.ApprovalRequest) {
	key := approvalGrantKey(request)
	a.mu.Lock()
	if a.approvalGrants == nil {
		a.approvalGrants = make(map[string]struct{})
	}
	a.approvalGrants[key] = struct{}{}
	a.mu.Unlock()
}
