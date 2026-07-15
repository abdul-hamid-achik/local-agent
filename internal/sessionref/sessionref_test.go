package sessionref

import "testing"

func TestFormat(t *testing.T) {
	for _, test := range []struct {
		id   int64
		want string
	}{
		{id: 7, want: "S7"},
		{id: 42, want: "S42"},
		{id: 0, want: ""},
		{id: -1, want: ""},
	} {
		if got := Format(test.id); got != test.want {
			t.Fatalf("Format(%d) = %q, want %q", test.id, got, test.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, value := range []string{"7", "S7", "s7"} {
		id, err := Parse(value)
		if err != nil || id != 7 {
			t.Fatalf("Parse(%q) = %d, %v; want 7, nil", value, id, err)
		}
	}
	for _, value := range []string{"", "S", "S0", "0", "-1", "+1", "01", "S01", " S7 ", "session-7", "S999999999999999999999"} {
		if id, err := Parse(value); err == nil || id != 0 {
			t.Fatalf("Parse(%q) = %d, %v; want invalid", value, id, err)
		}
	}
}
