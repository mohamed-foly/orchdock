package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// MaxAliasLen is the longest alias allowed.
const MaxAliasLen = 48

var aliasRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// ValidAlias reports whether s matches ^[a-z0-9][a-z0-9-]{0,47}$.
func ValidAlias(s string) bool { return aliasRe.MatchString(s) }

// CheckAlias returns an ErrInvalid error explaining why s is not a valid alias.
func CheckAlias(s string) error {
	if ValidAlias(s) {
		return nil
	}
	return invalid("alias", "%q must be 1-%d lowercase letters, digits or '-', starting with a letter or digit", s, MaxAliasLen)
}

// DeriveAlias turns a title into an alias: lowercase, every run of other
// characters becomes '-', trimmed, at most 48 characters; "item" if nothing is left.
func DeriveAlias(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxAliasLen {
		s = strings.Trim(s[:MaxAliasLen], "-")
	}
	if s == "" {
		s = "item"
	}
	return s
}

// UniqueAlias returns base, or the first of base-2, base-3, … (shortened to
// stay within 48 characters) that taken reports as free.
func UniqueAlias(base string, taken func(alias string) bool) string {
	base = strings.ToLower(base)
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > MaxAliasLen {
			stem = strings.TrimRight(stem[:MaxAliasLen-len(suffix)], "-")
		}
		if c := stem + suffix; !taken(c) {
			return c
		}
	}
}
