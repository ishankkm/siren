package event

import "testing"

func TestNormalizeSummary(t *testing.T) {
	cases := map[string]string{
		// Long digit/hex runs collapse; short ones (<3 digits, <8 hex) stay.
		"connection refused on 127.0.0.1:8080": "connection refused on <N>.0.0.1:<N>",
		"trace id abcdef0123456789 failed":     "trace id <HEX> failed",
		"short 12 ok":                          "short 12 ok",
		"err code 42":                          "err code 42",
	}
	for in, want := range cases {
		if got := NormalizeSummary(in); got != want {
			t.Errorf("NormalizeSummary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFingerprintStability(t *testing.T) {
	a := Fingerprint("bots", SourceLog, "boom")
	b := Fingerprint("bots", SourceLog, "boom")
	if a != b {
		t.Fatalf("expected stable fingerprint, got %q vs %q", a, b)
	}
	if Fingerprint("bots", SourceLog, "boom") == Fingerprint("bots", SourceProcess, "boom") {
		t.Fatalf("expected different fingerprints across sources")
	}
}

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"debug":   SeverityInfo,
		"INFO":    SeverityInfo,
		"warn":    SeverityWarn,
		"warning": SeverityWarn,
		"error":   SeverityError,
		"ERR":     SeverityError,
		"fatal":   SeverityCritical,
		"panic":   SeverityCritical,
		"":        SeverityError, // unknown -> error (safe default)
		"banana":  SeverityError,
	}
	for in, want := range cases {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", in, got, want)
		}
	}
}
