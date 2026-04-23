package command

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		kind Kind
		args []string
	}{
		{"!help", true, KindHelp, nil},
		{"  !ack abc  ", true, KindAck, []string{"abc"}},
		{"!mute bots 1h", true, KindMute, []string{"bots", "1h"}},
		{"!unmute bots", true, KindUnmute, []string{"bots"}},
		{"!status", true, KindStatus, nil},
		{"!nope", true, KindUnknown, nil},
		{"hello", false, "", nil},
		{"!", false, "", nil},
		{"   ", false, "", nil},
	}
	for _, c := range cases {
		got, ok := Parse(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Kind != c.kind {
			t.Errorf("%q: kind=%v, want %v", c.in, got.Kind, c.kind)
		}
		if len(got.Args) != len(c.args) {
			t.Errorf("%q: args=%v, want %v", c.in, got.Args, c.args)
			continue
		}
		for i := range got.Args {
			if got.Args[i] != c.args[i] {
				t.Errorf("%q: arg[%d]=%q, want %q", c.in, i, got.Args[i], c.args[i])
			}
		}
	}
}
