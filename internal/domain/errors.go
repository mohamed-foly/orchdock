// Package domain holds OrchDock's data model and its validation rules. It does
// no I/O: storage lives in catalog, execution in the runner.
package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Sentinel errors; callers test them with errors.Is.
var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrInvalid     = errors.New("invalid")
	ErrStartFailed = errors.New("failed to start")
)

// invalid returns an error wrapping ErrInvalid: "invalid: <field>: <message>".
func invalid(field, format string, a ...any) error {
	msg := fmt.Sprintf(format, a...)
	if field != "" {
		msg = field + ": " + msg
	}
	return fmt.Errorf("%w: %s", ErrInvalid, msg)
}

// collector accumulates validation errors under a field prefix.
type collector struct {
	prefix string
	errs   []error
}

func (c *collector) field(f string) string {
	if c.prefix == "" {
		return f
	}
	if f == "" {
		return c.prefix
	}
	return c.prefix + "." + f
}

func (c *collector) add(err error) {
	if err != nil {
		c.errs = append(c.errs, err)
	}
}

func (c *collector) addf(field, format string, a ...any) {
	c.errs = append(c.errs, invalid(c.field(field), format, a...))
}

// sub returns a collector that writes into c with a longer prefix.
func (c *collector) sub(f string) *collector { return &collector{prefix: c.field(f)} }

// merge adds the errors of a sub collector.
func (c *collector) merge(s *collector) { c.errs = append(c.errs, s.errs...) }

func (c *collector) err() error { return errors.Join(c.errs...) }

// prefixed puts field in front of the field path of every ErrInvalid error in err.
func prefixed(field string, err error) error {
	if err == nil {
		return nil
	}
	parts := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		parts = j.Unwrap()
	}
	out := make([]error, 0, len(parts))
	for _, p := range parts {
		if errors.Is(p, ErrInvalid) {
			msg := strings.TrimPrefix(p.Error(), ErrInvalid.Error()+": ")
			out = append(out, fmt.Errorf("%w: %s.%s", ErrInvalid, field, msg))
		} else {
			out = append(out, fmt.Errorf("%s: %w", field, p))
		}
	}
	return errors.Join(out...)
}

func itoa(i int) string { return strconv.Itoa(i) }
