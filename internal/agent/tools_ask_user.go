package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const (
	maxAskUserQuestionBytes = 2048
	maxAskUserOptions       = 8
	maxAskUserOptionID      = 64
	maxAskUserOptionLabel   = 256
	maxAskUserOptionDetail  = 512
	maxAskUserFreeText      = 1024
)

// UserQuestionKind is the interaction grammar for ask_user.
type UserQuestionKind string

const (
	UserQuestionYesNo    UserQuestionKind = "yes_no"
	UserQuestionSingle   UserQuestionKind = "single"
	UserQuestionFreeText UserQuestionKind = "freetext"
)

// UserQuestionOption is one bounded choice.
type UserQuestionOption struct {
	ID     string
	Label  string
	Detail string
}

// UserQuestionRequest is sent to the interactive UI and blocks until answered.
type UserQuestionRequest struct {
	CallID   string
	Kind     UserQuestionKind
	Prompt   string
	Options  []UserQuestionOption
	Response chan UserQuestionResponse
}

// UserQuestionResponse is the human's answer or skip.
type UserQuestionResponse struct {
	Skipped bool
	ID      string
	Label   string
	Text    string
}

func (a *Agent) SetQuestionCallback(cb func(UserQuestionRequest)) {
	a.mu.Lock()
	a.questionCallback = cb
	a.mu.Unlock()
}

func (a *Agent) questionCallbackOrNil() func(UserQuestionRequest) {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.questionCallback
}

func (a *Agent) handleAskUser(ctx context.Context, tc llm.ToolCall) (string, bool) {
	parsed, err := parseAskUser(tc.Arguments)
	if err != nil {
		return "ask_user: " + err.Error(), true
	}
	callback := a.questionCallbackOrNil()
	if callback == nil {
		return "ask_user requires an interactive session", true
	}
	parsed.CallID = tc.ID
	parsed.Response = make(chan UserQuestionResponse, 1)
	callback(parsed)
	select {
	case <-ctx.Done():
		return "ask_user cancelled: " + ctx.Err().Error(), true
	case answer := <-parsed.Response:
		return formatAskUserAnswer(parsed.Kind, answer), false
	}
}

func parseAskUser(args map[string]any) (UserQuestionRequest, error) {
	question, _ := args["question"].(string)
	question = strings.TrimSpace(question)
	if question == "" {
		return UserQuestionRequest{}, fmt.Errorf("question is required")
	}
	if utf8.RuneCountInString(question) > maxAskUserQuestionBytes {
		return UserQuestionRequest{}, fmt.Errorf("question is too long")
	}
	kind := UserQuestionKind(strings.TrimSpace(strings.ToLower(fmt.Sprint(args["kind"]))))
	if _, ok := args["kind"].(string); !ok {
		kind = ""
	}
	if kind == "" {
		if _, ok := args["options"]; ok {
			kind = UserQuestionSingle
		} else {
			kind = UserQuestionYesNo
		}
	}
	switch kind {
	case UserQuestionYesNo, UserQuestionSingle, UserQuestionFreeText:
	default:
		return UserQuestionRequest{}, fmt.Errorf("unknown kind %q", kind)
	}

	request := UserQuestionRequest{Kind: kind, Prompt: question}
	if kind == UserQuestionYesNo {
		request.Options = []UserQuestionOption{
			{ID: "yes", Label: "Yes"},
			{ID: "no", Label: "No"},
		}
		return request, nil
	}
	if kind == UserQuestionFreeText {
		return request, nil
	}

	raw, ok := args["options"].([]any)
	if !ok || len(raw) == 0 {
		return UserQuestionRequest{}, fmt.Errorf("single questions require options")
	}
	if len(raw) > maxAskUserOptions {
		return UserQuestionRequest{}, fmt.Errorf("too many options")
	}
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return UserQuestionRequest{}, fmt.Errorf("option must be an object")
		}
		id := strings.TrimSpace(fmt.Sprint(obj["id"]))
		label := strings.TrimSpace(fmt.Sprint(obj["label"]))
		detail, _ := obj["detail"].(string)
		if id == "" || label == "" {
			return UserQuestionRequest{}, fmt.Errorf("option id and label are required")
		}
		if utf8.RuneCountInString(id) > maxAskUserOptionID || utf8.RuneCountInString(label) > maxAskUserOptionLabel {
			return UserQuestionRequest{}, fmt.Errorf("option text is too long")
		}
		if utf8.RuneCountInString(detail) > maxAskUserOptionDetail {
			detail = string([]rune(detail)[:maxAskUserOptionDetail])
		}
		if _, dup := seen[id]; dup {
			return UserQuestionRequest{}, fmt.Errorf("duplicate option id %q", id)
		}
		seen[id] = struct{}{}
		request.Options = append(request.Options, UserQuestionOption{ID: id, Label: label, Detail: strings.TrimSpace(detail)})
	}
	if len(request.Options) < 2 {
		return UserQuestionRequest{}, fmt.Errorf("single questions need at least two options")
	}
	return request, nil
}

func formatAskUserAnswer(kind UserQuestionKind, answer UserQuestionResponse) string {
	payload := map[string]any{}
	if answer.Skipped {
		payload["skipped"] = true
	} else {
		payload["answered"] = true
		switch kind {
		case UserQuestionFreeText:
			payload["text"] = answer.Text
		default:
			payload["id"] = answer.ID
			payload["label"] = answer.Label
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"skipped":true}`
	}
	return string(encoded)
}
