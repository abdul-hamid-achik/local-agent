package ui

// ApprovalPosture is the process-wide approval contract selected by the host.
// It is presentation state only: the permission checker remains the execution
// authority, while the TUI makes that authority visible without inferring it
// from conversational mode.
type ApprovalPosture string

const (
	ApprovalPosturePrompted      ApprovalPosture = "prompted"
	ApprovalPostureSkipApprovals ApprovalPosture = "skip_approvals"
	// ApprovalPostureYolo is retained for source compatibility.
	// Deprecated: use ApprovalPostureSkipApprovals.
	ApprovalPostureYolo = ApprovalPostureSkipApprovals
)

func (p ApprovalPosture) valid() bool {
	return p == ApprovalPosturePrompted || p == ApprovalPostureSkipApprovals
}

// SetApprovalPosture projects the host's actual permission posture into the
// TUI. Invalid values fail closed to the ordinary prompted posture.
func (m *Model) SetApprovalPosture(posture ApprovalPosture) {
	if m == nil {
		return
	}
	if !posture.valid() {
		posture = ApprovalPosturePrompted
	}
	m.approvalPosture = posture
	if m.settingsPickerState != nil {
		m.refreshSettingsPicker()
	}
	if m.runtimeStatusState != nil {
		m.refreshRuntimeStatus(true)
	}
}

func (m *Model) skipApprovalsEnabled() bool {
	return m != nil && m.approvalPosture == ApprovalPostureSkipApprovals
}

func (m *Model) approvalPostureRuntimeLabel() string {
	if m.skipApprovalsEnabled() {
		return "Approval prompts skipped · host/tool boundaries apply"
	}
	return "Prompted for approval-gated tools"
}

func (m *Model) approvalPostureWelcomeLabel(compact bool) string {
	if m.skipApprovalsEnabled() {
		if compact {
			return "approval prompts skipped"
		}
		return "Approval prompts skipped · host/tool boundaries apply"
	}
	if compact {
		return "approval prompts on"
	}
	return "approval prompts enabled"
}

func (m *Model) approvalPostureWelcomeMicroLabel() string {
	if m.skipApprovalsEnabled() {
		return "prompts skipped"
	}
	return "prompts on"
}
