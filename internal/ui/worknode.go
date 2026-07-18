package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/local-agent/internal/expertteam"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
)

const (
	maxWorkNodeIDBytes    = 64
	maxWorkNodeLabelBytes = maxExpertProgressNameBytes
	maxWorkNodeModelBytes = maxExpertProgressModelBytes
)

// WorkNodeStatus is the bounded presentation lifecycle for one unit of agent
// work. It does not mirror provider events or grant arbitrary status text
// authority to a child agent.
type WorkNodeStatus uint8

const (
	WorkNodeQueued WorkNodeStatus = iota + 1
	WorkNodeRunning
	WorkNodeWaiting
	WorkNodeCompleted
	WorkNodeAttention
	WorkNodeFailed
	WorkNodeCancelled
)

// WorkNode is the safe, host-owned projection rendered for agent work.
//
// Deliberately absent: prompts, objectives, reports, reasoning, raw errors,
// private paths, transcripts, and arbitrary metadata.
type WorkNode struct {
	ID          string                  `json:"id"`
	Index       int                     `json:"index"`
	Label       string                  `json:"label,omitempty"`
	Model       string                  `json:"model,omitempty"`
	Location    llm.OllamaModelLocation `json:"location,omitempty"`
	Status      WorkNodeStatus          `json:"status"`
	FailureCode string                  `json:"failure_code,omitempty"`
	EvalTokens  int                     `json:"eval_tokens,omitempty"`
}

func (node WorkNode) valid(total int) bool {
	if total < 1 || total > maxExpertProgressItems || node.Index < 0 || node.Index >= total ||
		!validWorkNodeID(node.ID) || node.EvalTokens < 0 || node.EvalTokens > maxExpertProgressTokens ||
		!validExpertProgressLocation(node.Location) {
		return false
	}

	switch node.Status {
	case WorkNodeQueued:
		return node.Label == "" && node.Model == "" &&
			node.Location == llm.OllamaModelLocationUnknown &&
			node.FailureCode == "" && node.EvalTokens == 0
	case WorkNodeRunning, WorkNodeWaiting:
		return validWorkNodeIdentity(node) && node.FailureCode == "" && node.EvalTokens == 0
	case WorkNodeCompleted:
		return validWorkNodeIdentity(node) && node.FailureCode == ""
	case WorkNodeAttention, WorkNodeFailed:
		return validWorkNodeIdentity(node) && validExpertFailureCode(node.FailureCode)
	case WorkNodeCancelled:
		return validWorkNodeIdentity(node) && node.FailureCode == "cancelled"
	default:
		return false
	}
}

func validWorkNodeIdentity(node WorkNode) bool {
	return boundedExpertProgressIdentifier(node.Label, maxWorkNodeLabelBytes) &&
		boundedExpertProgressIdentifier(node.Model, maxWorkNodeModelBytes)
}

func validWorkNodeID(value string) bool {
	if !utf8.ValidString(value) || value == "" || len(value) > maxWorkNodeIDBytes ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || (r < 'a' || r > 'z') &&
			(r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			r != '-' && r != '_' && r != ':' {
			return false
		}
	}
	return true
}

// workNodesFromExpertProgress is a pure adapter from the consultation-specific
// state into the generic work projection. It clones and validates its input,
// assigns only host-derived IDs, and returns nodes in presentation order.
func workNodesFromExpertProgress(state *ExpertProgressState) ([]WorkNode, bool) {
	candidate := cloneExpertProgressState(state)
	if candidate != nil {
		// ExpertProgressState historically represents untouched queue slots with
		// the Go zero value. Normalize that internal absence to the explicit
		// public "unknown" location before applying the strict snapshot gate.
		for index := range candidate.Experts {
			item := &candidate.Experts[index]
			if item.Expert == "" && item.Model == "" && item.Phase == "" && item.Location == "" {
				item.Location = llm.OllamaModelLocationUnknown
			}
		}
	}
	safe := sanitizeExpertProgressState(candidate, false)
	if safe == nil {
		return nil, false
	}

	nodes := make([]WorkNode, safe.Total)
	for index, item := range safe.Experts {
		node := WorkNode{
			ID:       fmt.Sprintf("expert-%02d", index),
			Index:    index,
			Status:   WorkNodeQueued,
			Location: llm.OllamaModelLocationUnknown,
		}
		if item.Expert != "" {
			node.Label = item.Expert
			node.Model = item.Model
			node.Location = item.Location
			node.FailureCode = item.FailureCode
			node.EvalTokens = item.EvalTokens
			switch item.Phase {
			case expertteam.ProgressStarted:
				node.Status = WorkNodeRunning
			case expertteam.ProgressCompleted:
				node.Status = WorkNodeCompleted
			case expertteam.ProgressFailed:
				if item.FailureCode == "cancelled" {
					node.Status = WorkNodeCancelled
				} else {
					node.Status = WorkNodeFailed
				}
			default:
				return nil, false
			}
		}
		if !node.valid(safe.Total) {
			return nil, false
		}
		nodes[index] = node
	}
	return orderedWorkNodes(nodes), true
}

func orderedWorkNodes(nodes []WorkNode) []WorkNode {
	ordered := append([]WorkNode(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left.Index != right.Index {
			return left.Index < right.Index
		}
		return left.ID < right.ID
	})
	return ordered
}
