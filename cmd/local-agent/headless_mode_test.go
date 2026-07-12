package main

import (
	"testing"

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
		{name: "auto requires supervisor", value: "auto", headless: true, wantErr: true},
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
