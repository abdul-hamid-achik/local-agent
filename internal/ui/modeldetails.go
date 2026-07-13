package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderCompactOllamaModelDetails keeps the minimum terminal actionable. It
// projects one bounded line per decision-relevant group instead of letting the
// complete metadata table push the footer and closing border off-screen.
func renderCompactOllamaModelDetails(model OllamaModelDescriptor, width int, isDark bool) string {
	width = max(1, width)
	palette := outputSemanticPalette(isDark)
	titleStyle := lipgloss.NewStyle().Foreground(palette.Accent).Bold(true)
	textStyle := lipgloss.NewStyle().Foreground(palette.Text)
	dimStyle := lipgloss.NewStyle().Foreground(palette.Dim)
	warningStyle := lipgloss.NewStyle().Foreground(palette.Warning)

	appendLine := func(lines []string, style lipgloss.Style, value string) []string {
		value = strings.TrimSpace(value)
		if value == "" {
			return lines
		}
		return append(lines, style.Render(truncateDisplay(value, width)))
	}

	title := strings.TrimSpace(model.DisplayName)
	if title == "" {
		title = model.Name
	}
	lines := appendLine(nil, titleStyle, title)
	lines = appendLine(lines, textStyle, modelSelectionState(model))

	modelParts := make([]string, 0, 4)
	if model.ParameterSize != "" {
		modelParts = append(modelParts, model.ParameterSize)
	}
	if model.Quantization != "" {
		modelParts = append(modelParts, model.Quantization)
	}
	if size := humanModelBytes(model.SizeBytes); size != "" {
		modelParts = append(modelParts, size+" weights")
	}
	if size := humanModelBytes(model.SizeVRAM); size != "" {
		modelParts = append(modelParts, size+" VRAM")
	}
	if len(modelParts) > 0 {
		lines = appendLine(lines, dimStyle, "Model · "+strings.Join(modelParts, " · "))
	}

	contextParts := make([]string, 0, 3)
	if model.AllocatedContext > 0 {
		contextParts = append(contextParts, compactTokenCount(model.AllocatedContext)+" active")
	}
	if model.EffectiveContext > 0 && model.EffectiveContext != model.AllocatedContext {
		contextParts = append(contextParts, compactTokenCount(model.EffectiveContext)+" effective")
	}
	if model.ContextLength > 0 && model.ContextLength != model.EffectiveContext {
		contextParts = append(contextParts, compactTokenCount(model.ContextLength)+" max")
	}
	if len(contextParts) > 0 {
		lines = appendLine(lines, dimStyle, "Context · "+strings.Join(contextParts, " · "))
	}
	if capabilities := compactCapabilities(model.Capabilities); capabilities != "" {
		lines = appendLine(lines, dimStyle, "Can · "+strings.ReplaceAll(capabilities, "+", " · "))
	}
	if model.Reason != "" {
		lines = appendLine(lines, warningStyle, "Note · "+model.Reason)
	}
	if model.Source != OllamaModelLocal {
		lines = appendLine(lines, dimStyle, "Cloud · remote prompts")
	}
	return strings.Join(lines, "\n")
}

// renderOllamaModelDetails is a transport-independent details projection for a
// modal owned by the parent model. It remains useful while enrichment loads:
// unknown metadata is omitted instead of rendered as misleading zero values.
func renderOllamaModelDetails(model OllamaModelDescriptor, width int, isDark bool) string {
	width = max(24, width)
	palette := outputSemanticPalette(isDark)
	label := lipgloss.NewStyle().Foreground(palette.Dim).Width(min(15, width/3))
	value := lipgloss.NewStyle().Foreground(palette.Text)
	accent := lipgloss.NewStyle().Foreground(palette.Accent).Bold(true)
	status := "Available"
	statusColor := palette.Success
	if !model.Selectable {
		status, statusColor = "Unavailable", palette.Warning
	} else if !model.Fit {
		status, statusColor = "Outside default profile", palette.Warning
	} else if model.Running {
		status = "Running"
	}
	source := strings.ToLower(modelGroupLabel(descriptorGroup(OllamaModelDescriptor{Source: model.Source, Selectable: true, Fit: true})))
	rows := [][2]string{{"Source", source}, {"Status", status}}
	if model.ParameterSize != "" {
		rows = append(rows, [2]string{"Parameters", model.ParameterSize})
	}
	if model.Quantization != "" {
		rows = append(rows, [2]string{"Quantization", model.Quantization})
	}
	if size := humanModelBytes(model.SizeBytes); size != "" {
		rows = append(rows, [2]string{"Weights", size})
	}
	if model.ContextLength > 0 {
		rows = append(rows, [2]string{"Max context", compactTokenCount(model.ContextLength) + " tokens"})
	}
	if model.EffectiveContext > 0 {
		rows = append(rows, [2]string{"Effective", compactTokenCount(model.EffectiveContext) + " tokens"})
	}
	if model.AllocatedContext > 0 {
		rows = append(rows, [2]string{"Active context", compactTokenCount(model.AllocatedContext) + " tokens"})
	}
	if size := humanModelBytes(model.SizeVRAM); size != "" {
		rows = append(rows, [2]string{"VRAM", size})
	}
	if caps := compactCapabilities(model.Capabilities); caps != "" {
		rows = append(rows, [2]string{"Capabilities", caps})
	}

	var b strings.Builder
	title := model.DisplayName
	if title == "" {
		title = model.Name
	}
	b.WriteString(accent.Render(title))
	if model.Current {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(palette.Success).Render("✓ current"))
	}
	for _, row := range rows {
		b.WriteString("\n")
		b.WriteString(label.Render(row[0]))
		b.WriteString(" ")
		rowStyle := value
		if row[0] == "Status" {
			rowStyle = lipgloss.NewStyle().Foreground(statusColor)
		}
		b.WriteString(rowStyle.Render(row[1]))
	}
	if model.Reason != "" {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(palette.Warning).Render(model.Reason))
	}
	if model.Source != OllamaModelLocal {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(palette.Dim).Render("Prompts leave this machine through Ollama."))
	}
	return lipgloss.NewStyle().Width(width).Render(b.String())
}
