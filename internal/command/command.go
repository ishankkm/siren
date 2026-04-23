// Package command parses and dispatches operator-issued Discord DM commands.
//
// Supported in v1: !ack, !mute, !unmute, !status, !help. Only messages from
// the configured operator user ID are accepted; everything else is ignored.
package command

import (
	"strings"
)

// Kind enumerates the supported command verbs.
type Kind string

const (
	KindAck     Kind = "ack"
	KindMute    Kind = "mute"
	KindUnmute  Kind = "unmute"
	KindStatus  Kind = "status"
	KindHelp    Kind = "help"
	KindUnknown Kind = "unknown"
)

// Command is a parsed operator command.
type Command struct {
	Kind Kind
	Args []string
	Raw  string
}

// Parse extracts a Command from a raw DM body. Returns (Command, true) iff
// the message starts with the "!" prefix.
func Parse(body string) (Command, bool) {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "!") {
		return Command{}, false
	}
	fields := strings.Fields(body[1:])
	if len(fields) == 0 {
		return Command{}, false
	}
	verb := strings.ToLower(fields[0])
	c := Command{Args: fields[1:], Raw: body}
	switch verb {
	case "ack":
		c.Kind = KindAck
	case "mute":
		c.Kind = KindMute
	case "unmute":
		c.Kind = KindUnmute
	case "status":
		c.Kind = KindStatus
	case "help":
		c.Kind = KindHelp
	default:
		c.Kind = KindUnknown
	}
	return c, true
}
