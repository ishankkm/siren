package redact

import (
	"testing"

	"github.com/ishankkm/siren/internal/config"
)

func TestApply(t *testing.T) {
	r, err := New([]config.Redact{
		{Pattern: `(?i)bearer\s+\S+`, Replacement: "[AUTH]"},
		{Pattern: `[A-Za-z0-9]{32,}`}, // default replacement
	})
	if err != nil {
		t.Fatal(err)
	}
	in := "Bearer abc.def.ghi failed; token=" + string(make([]byte, 0)) + "0123456789abcdef0123456789abcdef"
	got := r.Apply(in)
	if got == in {
		t.Fatalf("expected redaction, got %q", got)
	}
}

func TestNilRedactor(t *testing.T) {
	var r *Redactor
	if got := r.Apply("abc"); got != "abc" {
		t.Fatalf("nil redactor must be passthrough, got %q", got)
	}
}

func TestBadPattern(t *testing.T) {
	if _, err := New([]config.Redact{{Pattern: "([unclosed"}}); err == nil {
		t.Fatal("expected error for bad pattern")
	}
}
