// Package redact applies an ordered list of regex rules to outbound text.
package redact

import (
	"fmt"
	"regexp"

	"github.com/ishankkm/siren/internal/config"
)

// Rule is a compiled redaction rule.
type Rule struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// Redactor applies rules in order.
type Redactor struct {
	rules []Rule
}

// New compiles the given config rules into a Redactor.
// An empty replacement defaults to "[REDACTED]".
func New(specs []config.Redact) (*Redactor, error) {
	rules := make([]Rule, 0, len(specs))
	for i, s := range specs {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			return nil, fmt.Errorf("redact rule %d: %w", i, err)
		}
		repl := s.Replacement
		if repl == "" {
			repl = "[REDACTED]"
		}
		rules = append(rules, Rule{Pattern: re, Replacement: repl})
	}
	return &Redactor{rules: rules}, nil
}

// Apply runs all rules over s in order.
func (r *Redactor) Apply(s string) string {
	if r == nil {
		return s
	}
	for _, rule := range r.rules {
		s = rule.Pattern.ReplaceAllString(s, rule.Replacement)
	}
	return s
}
