package event

import "testing"

func TestNormalizeSummary(t *testing.T) {
	cases := map[string]string{
		// Structured tokens collapse first.
		"connection refused on 127.0.0.1:8080":               "connection refused on <IP>:<N>",
		"peer 10.0.0.5 down":                                 "peer <IP> down",
		"trace id abcdef0123456789 failed":                   "trace id <TOKEN> failed",
		"short hex deadbee survives":                         "short hex deadbee survives",
		"req 550e8400-e29b-41d4-a716-446655440000 timed out": "req <UUID> timed out",
		"REQ AABBCCDD-EEFF-0011-2233-445566778899 timed out": "REQ <UUID> timed out",
		"connect [::1]:5432 refused":                         "connect [<IP>]:<N> refused",
		"connect [fe80::1ff:fe23:4567:890a]:22 refused":      "connect [<IP>]:22 refused",
		// Short numeric/hex runs survive untouched.
		"short 12 ok": "short 12 ok",
		"err code 42": "err code 42",
		// Per-request high-cardinality lines collapse together.
		"failed peer 192.168.1.5 req 550e8400-e29b-41d4-a716-446655440000": "failed peer <IP> req <UUID>",
		"failed peer 192.168.1.6 req 550e8400-e29b-41d4-a716-446655440001": "failed peer <IP> req <UUID>",
	}
	for in, want := range cases {
		if got := NormalizeSummary(in); got != want {
			t.Errorf("NormalizeSummary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSummaryDedupesHighCardinality(t *testing.T) {
	// The whole point: per-request error lines must produce one fingerprint.
	a := NormalizeSummary("auth failed for user 192.168.1.5 token abc123def456ghi789")
	b := NormalizeSummary("auth failed for user 10.0.0.99 token xyz999aaa888bbb777")
	if a != b {
		t.Fatalf("expected identical normalized summaries, got %q vs %q", a, b)
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
