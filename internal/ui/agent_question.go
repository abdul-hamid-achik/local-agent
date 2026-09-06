package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
)

type pendingAgentQuestion struct {
	CallID   string
	Prompt   QuestionPrompt
	Response chan<- AgentQuestionResponse
}

func mapUserQuestion(req agent.UserQuestionRequest) AgentQuestionMsg {
	msg := AgentQuestionMsg{
		CallID: req.CallID,
		Prompt: req.Prompt,
		Response: func() chan<- AgentQuestionResponse {
			out := make(chan AgentQuestionResponse, 1)
			go func() {
				answer := <-out
				req.Response <- agent.UserQuestionResponse{
					Skipped: answer.Skipped,
					ID:      answer.ID,
					Label:   answer.Label,
					Text:    answer.Text,
				}
			}()
			return out
		}(),
	}
	switch req.Kind {
	case agent.UserQuestionYesNo:
		msg.Kind = QuestionKindYesNo
	case agent.UserQuestionFreeText:
		msg.Kind = QuestionKindFreeText
	default:
		msg.Kind = QuestionKindSingle
	}
	for _, option := range req.Options {
		msg.Options = append(msg.Options, QuestionChoice{ID: option.ID, Label: option.Label, Detail: option.Detail})
	}
	return msg
}

func (m *Model) handleAgentQuestionRequest(msg AgentQuestionMsg) {
	if m.shuttingDown {
		if msg.Response != nil {
			select {
			case msg.Response <- AgentQuestionResponse{Skipped: true}:
			default:
			}
		}
		return
	}
	m.openAgentQuestion(msg)
}

func (m *Model) openAgentQuestion(msg AgentQuestionMsg) {
	m.preemptTranscriptSearch()
	if m.overlay == OverlayPlanForm || m.overlay == OverlayGoalForm {
		m.planFormState = nil
		m.goalFormState = nil
		m.overlay = OverlayNone
	}
	if m.isCompletionActive() {
		m.dismissCompletion()
	}
	prompt := newQuestionPrompt(msg.Kind, "Question", msg.Prompt, msg.Options, m.styles, m.glyphProfile)
	prompt.SetWidth(m.width)
	if msg.Kind == QuestionKindFreeText {
		prompt.initFreeText(m.isDark, m.themeID, m.reducedMotion)
	}
	m.pendingQuestion = &pendingAgentQuestion{
		CallID:   msg.CallID,
		Prompt:   prompt,
		Response: msg.Response,
	}
	m.overlayParent = OverlayNone
	if m.overlay != OverlayCortexDecision {
		m.overlay = OverlayNone
	}
	m.input.Blur()
	m.recalcViewportHeight()
}

func (m *Model) resolveAgentQuestion(answer AgentQuestionResponse) {
	if m.pendingQuestion == nil {
		return
	}
	if m.pendingQuestion.Response != nil {
		select {
		case m.pendingQuestion.Response <- answer:
		default:
		}
	}
	m.pendingQuestion = nil
	if m.composerEditable() && m.pendingApproval == nil {
		m.input.Focus()
	}
	m.recalcViewportHeight()
}

func (m *Model) renderAgentQuestion() string {
	if m.pendingQuestion == nil {
		return ""
	}
	prompt := m.pendingQuestion.Prompt
	prompt.SetWidth(m.width)
	return dialogFrame(m.styles, prompt.Render(), prompt.Footer(), m.width, m.glyphProfile)
}

func (m *Model) handleAgentQuestionKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if m.pendingQuestion == nil {
		return nil, false
	}
	prompt := &m.pendingQuestion.Prompt
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.resolveAgentQuestion(AgentQuestionResponse{Skipped: true})
		return m.beginShutdown(), true
	case key.Matches(msg, m.keys.Cancel):
		m.resolveAgentQuestion(AgentQuestionResponse{Skipped: true})
		return nil, true
	case prompt.Kind == QuestionKindFreeText:
		if key.Matches(msg, m.keys.Send) {
			text := strings.TrimSpace(prompt.Text())
			if text == "" {
				prompt.Notice = "Type an answer, or esc to skip"
				return nil, true
			}
			m.resolveAgentQuestion(AgentQuestionResponse{Text: text})
			return nil, true
		}
		prompt.UpdateInput(msg)
		return nil, true
	case key.Matches(msg, m.keys.CompleteUp), strings.EqualFold(msg.String(), "k"):
		prompt.Move(-1)
	case key.Matches(msg, m.keys.CompleteDown), strings.EqualFold(msg.String(), "j"):
		prompt.Move(1)
	case key.Matches(msg, m.keys.CompleteSelect):
		choice, ok := prompt.Choice()
		if !ok {
			prompt.Notice = "Choose an option with ↑/↓ or j/k first."
			return nil, true
		}
		m.resolveAgentQuestion(AgentQuestionResponse{ID: choice.ID, Label: choice.Label})
	case strings.EqualFold(msg.String(), "y") && prompt.Kind == QuestionKindYesNo:
		for _, choice := range prompt.Choices {
			if strings.EqualFold(choice.ID, "yes") {
				m.resolveAgentQuestion(AgentQuestionResponse{ID: choice.ID, Label: choice.Label})
				return nil, true
			}
		}
	case strings.EqualFold(msg.String(), "n") && prompt.Kind == QuestionKindYesNo:
		for _, choice := range prompt.Choices {
			if strings.EqualFold(choice.ID, "no") {
				m.resolveAgentQuestion(AgentQuestionResponse{ID: choice.ID, Label: choice.Label})
				return nil, true
			}
		}
	}
	return nil, true
}
