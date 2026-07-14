package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/expertselector"
	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
)

const maxExpertObjectiveBytes = 32 * 1024

func (a *Agent) hasExpertConsultant() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	available := a.expertConsultant != nil
	a.mu.RUnlock()
	return available
}

// ExpertConsultationAvailable reports whether the host installed the bounded,
// tool-free Team/Swarm/MoE runtime. It exposes availability only, never the
// consultant implementation or its model/profile configuration.
func (a *Agent) ExpertConsultationAvailable() bool {
	return a.hasExpertConsultant()
}

// ExpertConsultationProfileCount reports a bounded catalog count when the
// installed runtime exposes one. Custom embedders that do not implement this
// optional surface still appear as available without leaking configuration.
func (a *Agent) ExpertConsultationProfileCount() int {
	if a == nil {
		return 0
	}
	a.mu.RLock()
	consultant := a.expertConsultant
	a.mu.RUnlock()
	counter, ok := consultant.(interface{ ProfileCount() int })
	if !ok {
		return 0
	}
	return max(0, counter.ProfileCount())
}

func (a *Agent) preflightConsultExperts(arguments map[string]any) error {
	if !a.hasExpertConsultant() {
		return errors.New("expert consultation is unavailable")
	}
	request, err := expertRequest(arguments)
	if err != nil {
		return err
	}
	if len(arguments) < 2 || len(arguments) > 3 {
		return errors.New("consult_experts accepts only strategy, objective, and optional experts")
	}
	for key := range arguments {
		if key != "strategy" && key != "objective" && key != "experts" {
			return errors.New("consult_experts contains an unknown argument")
		}
	}
	if !boundedExpertText(request.Objective, maxExpertObjectiveBytes, false) {
		return errors.New("objective is empty, invalid, or too large")
	}
	for _, name := range request.ExpertNames {
		if !boundedExpertText(name, 128, true) || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\r\n") {
			return errors.New("experts contains an invalid profile name")
		}
	}
	return nil
}

func (a *Agent) handleConsultExperts(ctx context.Context, arguments map[string]any) (string, bool) {
	content, isErr, _, usageErr := a.handleConsultExpertsWithBudget(ctx, arguments, 0)
	if usageErr != nil {
		return "error: expert consultation returned an invalid usage receipt", true
	}
	return content, isErr
}

func (a *Agent) handleConsultExpertsWithBudget(ctx context.Context, arguments map[string]any, maxTotalEvalTokens int) (string, bool, expertteam.Usage, error) {
	request, err := expertRequest(arguments)
	if err != nil {
		return "error: " + err.Error(), true, expertteam.Usage{}, nil
	}
	request.MaxTotalEvalTokens = maxTotalEvalTokens
	a.mu.RLock()
	consultant := a.expertConsultant
	a.mu.RUnlock()
	if consultant == nil {
		return "error: expert consultation is unavailable", true, expertteam.Usage{}, nil
	}
	result, err := consultant.Consult(ctx, request)
	usage, usageErr := result.ChargedUsage()
	if usageErr != nil {
		return "error: expert consultation returned an invalid usage receipt", true, expertteam.Usage{}, usageErr
	}
	formatted := result.Format()
	if err == nil {
		return formatted, false, usage, nil
	}
	code := "expert consultation failed"
	switch {
	case errors.Is(err, context.Canceled):
		code = "expert consultation cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "expert consultation timed out"
	case errors.Is(err, expertteam.ErrAllExpertsFailed):
		code = "all selected experts failed"
	case errors.Is(err, expertteam.ErrInvalidRequest):
		code = "expert consultation request was rejected"
	case errors.Is(err, expertteam.ErrUnavailable):
		code = "expert consultation is unavailable"
	}
	if formatted == "" {
		return "error: " + code, true, usage, nil
	}
	return fmt.Sprintf("error: %s\n%s", code, formatted), true, usage, nil
}

func expertRequest(arguments map[string]any) (expertteam.Request, error) {
	strategyValue, ok := arguments["strategy"].(string)
	if !ok {
		return expertteam.Request{}, errors.New("strategy must be team, swarm, or moe")
	}
	strategy := expertselector.Strategy(strings.ToLower(strings.TrimSpace(strategyValue)))
	if strategy != expertselector.StrategyTeam && strategy != expertselector.StrategySwarm && strategy != expertselector.StrategyMoE {
		return expertteam.Request{}, errors.New("strategy must be team, swarm, or moe")
	}
	objective, ok := arguments["objective"].(string)
	if !ok || strings.TrimSpace(objective) == "" {
		return expertteam.Request{}, errors.New("objective must be a non-empty string")
	}
	names, err := expertNames(arguments["experts"])
	if err != nil {
		return expertteam.Request{}, err
	}
	return expertteam.Request{Strategy: strategy, Objective: objective, ExpertNames: names}, nil
}

func expertNames(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	var names []string
	switch values := value.(type) {
	case []string:
		names = append([]string(nil), values...)
	case []any:
		names = make([]string, 0, len(values))
		for _, value := range values {
			name, ok := value.(string)
			if !ok {
				return nil, errors.New("experts must contain only profile names")
			}
			names = append(names, name)
		}
	default:
		return nil, errors.New("experts must be an array of profile names")
	}
	if len(names) > expertselector.MaxSelectedExperts {
		return nil, fmt.Errorf("experts supports at most %d names", expertselector.MaxSelectedExperts)
	}
	return names, nil
}

func boundedExpertText(value string, limit int, singleLine bool) bool {
	if !utf8.ValidString(value) || len(value) > limit || strings.TrimSpace(value) == "" ||
		(singleLine && strings.ContainsAny(value, "\r\n")) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && (!singleLine && (character == '\n' || character == '\r' || character == '\t')) {
			continue
		}
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
