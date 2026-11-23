package domain

import (
	"fmt"
	"regexp"
	"slices"
)

// DefaultVariantID names the variant that always exists implicitly (the base
// command with nothing changed). It is the only reserved id; no other variant
// id or flag has any meaning to OrchDock.
const DefaultVariantID = "default"

// Variant is a free-form, user-defined way to run a command: any number per
// item or agent profile, any label. It may replace the command (Line or Argv),
// add arguments (PrependArgs right after the executable, AppendArgs at the
// end) and set environment variables (merged last).
type Variant struct {
	ID          string            `json:"id"`
	Label       string            `json:"label,omitempty"`
	Line        string            `json:"line,omitempty"`
	Argv        []string          `json:"argv,omitempty"`
	AppendArgs  []string          `json:"appendArgs,omitempty"`
	PrependArgs []string          `json:"prependArgs,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

var variantIDRe = regexp.MustCompile(`^[a-z0-9-]{1,24}$`)

// ValidVariantID reports whether id matches ^[a-z0-9-]{1,24}$.
func ValidVariantID(id string) bool { return variantIDRe.MatchString(id) }

func (c *collector) variants(field string, vs []Variant) {
	seen := map[string]bool{}
	for i, v := range vs {
		f := fmt.Sprintf("%s[%d]", field, i)
		if !ValidVariantID(v.ID) {
			c.addf(f+".id", "%q must be 1-24 lowercase letters, digits or '-'", v.ID)
		} else if seen[v.ID] {
			c.addf(f+".id", "duplicate variant id %q", v.ID)
		}
		seen[v.ID] = true
		if v.Line != "" && v.Argv != nil {
			c.addf(f, "set either line or argv, not both")
		}
		if v.Argv != nil && (len(v.Argv) == 0 || v.Argv[0] == "") {
			c.addf(f+".argv", "the executable must not be empty")
		}
		c.placeholders(f+".line", v.Line)
		c.placeholders(f+".argv", v.Argv...)
		c.placeholders(f+".appendArgs", v.AppendArgs...)
		c.placeholders(f+".prependArgs", v.PrependArgs...)
		c.env(f+".env", v.Env)
	}
}

// ValidateVariants checks a variant list on its own (ids, uniqueness, line/argv, placeholders, env names).
func ValidateVariants(vs []Variant) error {
	var c collector
	c.variants("variants", vs)
	return c.err()
}

// MergeVariants merges a profile's variants with an item's: same id → the
// item's variant wins (in the profile's position); item-only variants follow.
// The implicit default variant is added first when neither defines it.
func MergeVariants(profile, item []Variant) []Variant {
	out := make([]Variant, 0, len(profile)+len(item)+1)
	idx := map[string]int{}
	put := func(v Variant) {
		if i, ok := idx[v.ID]; ok {
			out[i] = v
			return
		}
		idx[v.ID] = len(out)
		out = append(out, v)
	}
	for _, v := range profile {
		put(v)
	}
	for _, v := range item {
		put(v)
	}
	if _, ok := idx[DefaultVariantID]; !ok {
		out = slices.Insert(out, 0, Variant{ID: DefaultVariantID, Label: "Default"})
	}
	return out
}

// ResolveVariant picks the variant to run: the chosen one, else the item's
// default, else the profile's default, else the implicit default. A named
// variant that does not exist is ErrNotFound.
func ResolveVariant(merged []Variant, chosen, itemDefault, profileDefault string) (Variant, error) {
	id := DefaultVariantID
	for _, c := range []string{chosen, itemDefault, profileDefault} {
		if c != "" {
			id = c
			break
		}
	}
	for _, v := range merged {
		if v.ID == id {
			return v, nil
		}
	}
	if id == DefaultVariantID {
		return Variant{ID: DefaultVariantID, Label: "Default"}, nil
	}
	return Variant{}, fmt.Errorf("variant %q: %w", id, ErrNotFound)
}
