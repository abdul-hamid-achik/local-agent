package main

import (
	"testing"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/ui"
)

func TestParseHeadlessMode(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		headless bool
		want     ui.Mode
		wantErr  bool
	}{
		{name: "default", value: "", headless: true, want: ui.ModeNormal},
		{name: "normal", value: " NORMAL ", headless: true, want: ui.ModeNormal},
		{name: "plan", value: "plan", headless: true, want: ui.ModePlan},
		{name: "auto", value: "auto", headless: true, want: ui.ModeAuto},
		{name: "unknown", value: "build", headless: true, wantErr: true},
		{name: "interactive flag unsupported", value: "plan", headless: false, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseHeadlessMode(test.value, test.headless)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseHeadlessMode() error = %v, wantErr=%v", err, test.wantErr)
			}
			if !test.wantErr && got != test.want {
				t.Fatalf("parseHeadlessMode() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHeadlessAuthorityMode(t *testing.T) {
	tests := []struct {
		mode ui.Mode
		want agent.AuthorityMode
	}{
		{mode: ui.ModeNormal, want: agent.AuthorityNormal},
		{mode: ui.ModePlan, want: agent.AuthorityPlan},
		{mode: ui.ModeAuto, want: agent.AuthorityAutoScoped},
		{mode: ui.Mode(99), want: agent.AuthorityNormal},
	}
	for _, test := range tests {
		if got := headlessAuthorityMode(test.mode); got != test.want {
			t.Fatalf("headless authority for %v = %v, want %v", test.mode, got, test.want)
		}
	}
}
