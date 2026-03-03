package tui

import "testing"

func TestSerializeDeserialize_Roundtrip(t *testing.T) {
	entries := []ChatEntry{
		{Kind: "user", Content: "Hello there"},
		{Kind: "assistant", Content: "Hi! How can I help?"},
		{Kind: "system", Content: "Model switched to qwen3"},
	}

	serialized := serializeEntries(entries)
	deserialized := deserializeEntries(serialized)

	if len(deserialized) != len(entries) {
		t.Fatalf("roundtrip length: got %d, want %d", len(deserialized), len(entries))
	}

	for i, e := range deserialized {
		if e.Kind != entries[i].Kind {
			t.Errorf("entry[%d] kind: got %q, want %q", i, e.Kind, entries[i].Kind)
		}
		if e.Content != entries[i].Content {
			t.Errorf("entry[%d] content: got %q, want %q", i, e.Content, entries[i].Content)
		}
	}
}

func TestSerializeEntries_Empty(t *testing.T) {
	result := serializeEntries(nil)
	if result != "" {
		t.Errorf("nil entries should serialize to empty, got %q", result)
	}
}

func TestDeserializeEntries_Empty(t *testing.T) {
	result := deserializeEntries("")
	if result != nil {
		t.Errorf("empty content should deserialize to nil, got %v", result)
	}
}

func TestDeserializeEntries_UnknownHeader(t *testing.T) {
	content := "## Unknown\n\nSome content\n\n## User\n\nValid content"
	result := deserializeEntries(content)
	if len(result) != 1 {
		t.Fatalf("should skip unknown headers, got %d entries", len(result))
	}
	if result[0].Kind != "user" {
		t.Errorf("should parse valid entry, got kind %q", result[0].Kind)
	}
}

func TestSerializeEntries_ErrorKind(t *testing.T) {
	entries := []ChatEntry{
		{Kind: "error", Content: "Something went wrong"},
	}
	serialized := serializeEntries(entries)
	if serialized == "" {
		t.Error("error entries should serialize")
	}

	deserialized := deserializeEntries(serialized)
	if len(deserialized) != 1 || deserialized[0].Kind != "error" {
		t.Errorf("error entry should roundtrip, got %v", deserialized)
	}
}

func TestSerializeEntries_MultilineContent(t *testing.T) {
	entries := []ChatEntry{
		{Kind: "user", Content: "line1\nline2\nline3"},
	}
	serialized := serializeEntries(entries)
	deserialized := deserializeEntries(serialized)
	if len(deserialized) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(deserialized))
	}
	if deserialized[0].Content != "line1\nline2\nline3" {
		t.Errorf("multiline content should roundtrip, got %q", deserialized[0].Content)
	}
}

func TestNotedAvailable(t *testing.T) {
	// Just ensure it doesn't panic.
	_ = notedAvailable()
}
