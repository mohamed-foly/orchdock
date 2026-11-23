package domain

import (
	"regexp"
	"slices"
	"strings"
)

// Placeholders that may appear in commands, paths, env values and URLs.
// They are expanded at run time and never stored expanded.
var Placeholders = []string{"port", "projectDir", "projectName", "alias", "itemId", "home", "sessionId", "newSessionId", "name"}

// Only {identifier} counts as a placeholder, so script blocks such as
// PowerShell's `{ $_.Name }` or JSON objects in arguments are left alone.
var placeholderRe = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)

// UnknownPlaceholders returns the placeholder names used in s that are not known.
func UnknownPlaceholders(s string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(Placeholders, m[1]) && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// CheckPlaceholders returns an ErrInvalid error if s uses an unknown placeholder.
func CheckPlaceholders(field, s string) error {
	if u := UnknownPlaceholders(s); len(u) > 0 {
		return invalid(field, "unknown placeholder {%s} (known: {%s})", strings.Join(u, "}, {"), strings.Join(Placeholders, "}, {"))
	}
	return nil
}

func (c *collector) placeholders(field string, values ...string) {
	for _, v := range values {
		if u := UnknownPlaceholders(v); len(u) > 0 {
			c.addf(field, "unknown placeholder {%s} (known: {%s})", strings.Join(u, "}, {"), strings.Join(Placeholders, "}, {"))
		}
	}
}

func (c *collector) placeholdersMap(field string, m map[string]string) {
	for _, k := range sortedKeys(m) {
		c.placeholders(field+"."+k, m[k])
	}
}

// Expand replaces placeholders that have a value in values; others stay as written.
func Expand(s string, values map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := values[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// ExpandAll expands every element of list.
func ExpandAll(list []string, values map[string]string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = Expand(s, values)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
