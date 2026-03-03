package tui

import (
	"testing"

	"github.com/abdulachik/local-agent/internal/command"
)

func TestCompleter_Complete(t *testing.T) {
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg)
	c := NewCompleter(reg, []string{"model-a"}, []string{"skill-a", "skill-b"}, []string{"agent-x"})

	t.Run("slash_dispatches_to_commands", func(t *testing.T) {
		results := c.Complete("/h")
		if len(results) == 0 {
			t.Error("expected command completions for /h")
		}
		for _, r := range results {
			if r.Category != "command" {
				t.Errorf("expected category 'command', got %q", r.Category)
			}
		}
	})

	t.Run("at_dispatches_to_agents", func(t *testing.T) {
		results := c.Complete("@agent")
		found := false
		for _, r := range results {
			if r.Category == "agent" {
				found = true
			}
		}
		if !found {
			t.Error("expected agent completions for @agent")
		}
	})

	t.Run("hash_dispatches_to_skills", func(t *testing.T) {
		results := c.Complete("#skill")
		if len(results) == 0 {
			t.Error("expected skill completions for #skill")
		}
		for _, r := range results {
			if r.Category != "skill" {
				t.Errorf("expected category 'skill', got %q", r.Category)
			}
		}
	})

	t.Run("plain_returns_nothing", func(t *testing.T) {
		results := c.Complete("hello")
		if len(results) != 0 {
			t.Errorf("expected no completions for plain text, got %d", len(results))
		}
	})
}

func TestCompleteCommand(t *testing.T) {
	reg := command.NewRegistry()
	command.RegisterBuiltins(reg)
	c := NewCompleter(reg, nil, nil, nil)

	t.Run("prefix_matching", func(t *testing.T) {
		results := c.Complete("/hel")
		found := false
		for _, r := range results {
			if r.Insert == "/help " {
				found = true
			}
		}
		if !found {
			t.Error("expected /help completion for prefix /hel")
		}
	})

	t.Run("alias_matching", func(t *testing.T) {
		// /h is an alias for /help
		results := c.Complete("/h")
		if len(results) == 0 {
			t.Error("expected completions for /h (alias)")
		}
	})

	t.Run("usage_suffix_in_label", func(t *testing.T) {
		// /model has Usage: "/model [name|list|fast|smart]"
		results := c.Complete("/model")
		for _, r := range results {
			if r.Insert == "/model " {
				// The label should include usage args from the Usage field.
				if r.Label == "/model" {
					// Label should have usage suffix if Usage has args.
					// Actually, let's check what the code does:
					// The code checks if cmd.Usage has >1 field.
					// "/model [name|list|fast|smart]" -> fields: ["/model", "[name|list|fast|smart]"]
					// So label should be "/model [name|list|fast|smart]"
					t.Error("label should include usage args")
				}
			}
		}
	})

	t.Run("no_matches", func(t *testing.T) {
		results := c.Complete("/zzzzz")
		if len(results) != 0 {
			t.Errorf("expected no completions for /zzzzz, got %d", len(results))
		}
	})
}

func TestCompleteSkill(t *testing.T) {
	reg := command.NewRegistry()
	c := NewCompleter(reg, nil, []string{"coding", "writing", "debugging"}, nil)

	t.Run("prefix_matching", func(t *testing.T) {
		results := c.Complete("#cod")
		if len(results) != 1 {
			t.Fatalf("expected 1 match for #cod, got %d", len(results))
		}
		if results[0].Label != "#coding" {
			t.Errorf("expected '#coding', got %q", results[0].Label)
		}
		if results[0].Category != "skill" {
			t.Errorf("expected category 'skill', got %q", results[0].Category)
		}
	})

	t.Run("all_match_empty_prefix", func(t *testing.T) {
		results := c.Complete("#")
		if len(results) != 3 {
			t.Errorf("expected 3 matches for #, got %d", len(results))
		}
	})

	t.Run("no_matches", func(t *testing.T) {
		results := c.Complete("#zzz")
		if len(results) != 0 {
			t.Errorf("expected no matches for #zzz, got %d", len(results))
		}
	})
}

func TestCompleterUpdateModels(t *testing.T) {
	reg := command.NewRegistry()
	c := NewCompleter(reg, []string{"old-model"}, nil, nil)

	c.UpdateModels([]string{"new-model-a", "new-model-b"})

	if len(c.models) != 2 {
		t.Errorf("expected 2 models, got %d", len(c.models))
	}
	if c.models[0] != "new-model-a" {
		t.Errorf("expected 'new-model-a', got %q", c.models[0])
	}
}

func TestCompleterUpdateAgents(t *testing.T) {
	reg := command.NewRegistry()
	c := NewCompleter(reg, nil, nil, []string{"old-agent"})

	c.UpdateAgents([]string{"new-agent"})

	if len(c.agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(c.agents))
	}
	if c.agents[0] != "new-agent" {
		t.Errorf("expected 'new-agent', got %q", c.agents[0])
	}
}
