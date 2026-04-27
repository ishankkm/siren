package journal

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ishankkm/siren/internal/event"
)

func newTestCollector(t *testing.T, regex string, maxPri int) *Collector {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := New("svc", "bots.service", maxPri, regex, logger)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClassifyPriorityFilter(t *testing.T) {
	c := newTestCollector(t, "", 3)
	// PRIORITY=4 (warning) is above the err threshold and must be dropped.
	if _, ok := c.classify([]byte(`{"MESSAGE":"slow query","PRIORITY":"4"}`)); ok {
		t.Fatal("expected PRIORITY=4 to be filtered out")
	}
	// PRIORITY=3 (err) passes.
	if _, ok := c.classify([]byte(`{"MESSAGE":"db down","PRIORITY":"3"}`)); !ok {
		t.Fatal("expected PRIORITY=3 to pass")
	}
}

func TestClassifyRegexFilter(t *testing.T) {
	c := newTestCollector(t, `(?i)\b(panic|fatal)\b`, 7)
	if _, ok := c.classify([]byte(`{"MESSAGE":"hello world","PRIORITY":"3"}`)); ok {
		t.Fatal("non-matching message should be dropped")
	}
	ev, ok := c.classify([]byte(`{"MESSAGE":"runtime panic in handler","PRIORITY":"3"}`))
	if !ok {
		t.Fatal("matching message should pass")
	}
	if ev.Source != event.SourceJournal {
		t.Errorf("source = %q, want journal", ev.Source)
	}
	if !strings.Contains(ev.Summary, "bots.service") {
		t.Errorf("summary should include unit, got %q", ev.Summary)
	}
}

func TestClassifySeverityMap(t *testing.T) {
	c := newTestCollector(t, "", 7)
	cases := []struct {
		pri  string
		want event.Severity
	}{
		{"0", event.SeverityCritical},
		{"2", event.SeverityCritical},
		{"3", event.SeverityError},
		{"4", event.SeverityWarn},
		{"6", event.SeverityInfo},
	}
	for _, tc := range cases {
		ev, ok := c.classify([]byte(`{"MESSAGE":"x","PRIORITY":"` + tc.pri + `"}`))
		if !ok {
			t.Fatalf("priority %s: dropped unexpectedly", tc.pri)
		}
		if ev.Severity != tc.want {
			t.Errorf("priority %s: severity = %v, want %v", tc.pri, ev.Severity, tc.want)
		}
	}
}

func TestClassifyTimestampParsed(t *testing.T) {
	c := newTestCollector(t, "", 7)
	// 2020-01-02T03:04:05Z in microseconds.
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	usec := want.UnixMicro()
	line := []byte(`{"MESSAGE":"x","PRIORITY":"3","__REALTIME_TIMESTAMP":"` + itoa(usec) + `"}`)
	ev, ok := c.classify(line)
	if !ok {
		t.Fatal("classify dropped record")
	}
	if !ev.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", ev.Timestamp, want)
	}
}

func TestClassifyMissingMessage(t *testing.T) {
	c := newTestCollector(t, "", 7)
	if _, ok := c.classify([]byte(`{"PRIORITY":"3"}`)); ok {
		t.Fatal("record without MESSAGE should be dropped")
	}
}

func TestClassifyInvalidJSON(t *testing.T) {
	c := newTestCollector(t, "", 7)
	if _, ok := c.classify([]byte(`not-json`)); ok {
		t.Fatal("invalid JSON should be dropped silently")
	}
}

func TestClassifyFingerprintStable(t *testing.T) {
	c := newTestCollector(t, "", 7)
	a, _ := c.classify([]byte(`{"MESSAGE":"connection refused 12345","PRIORITY":"3"}`))
	b, _ := c.classify([]byte(`{"MESSAGE":"connection refused 67890","PRIORITY":"3"}`))
	if a.Fingerprint != b.Fingerprint {
		t.Errorf("fingerprints should match across volatile digits: %s vs %s", a.Fingerprint, b.Fingerprint)
	}
}

func TestScanEmitsAndStops(t *testing.T) {
	c := newTestCollector(t, "", 3)
	input := strings.NewReader(
		`{"MESSAGE":"db down","PRIORITY":"3"}` + "\n" +
			`{"MESSAGE":"noisy debug","PRIORITY":"7"}` + "\n" +
			`{"MESSAGE":"panic!","PRIORITY":"2"}` + "\n",
	)
	ch := make(chan event.Event, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.scan(ctx, input, ch); err != nil {
		t.Fatal(err)
	}
	close(ch)
	var got []string
	for ev := range ch {
		got = append(got, ev.Detail)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events (the two below-threshold ones), got %d: %v", len(got), got)
	}
}

func TestRunNoJournalctl(t *testing.T) {
	if _, err := exec.LookPath("journalctl"); err == nil {
		t.Skip("journalctl is present; this test asserts the absent-binary path")
	}
	c := newTestCollector(t, "", 3)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx, make(chan event.Event, 1)); err != nil {
		t.Fatalf("Run should return nil when journalctl is missing, got %v", err)
	}
}

// itoa avoids a strconv import collision with the test file's other helpers.
func itoa(n int64) string {
	// Decimal, no allocation-sensitive path needed for tests.
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
