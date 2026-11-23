package domain

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestNewIDPrefixesAndUniqueness(t *testing.T) {
	for _, prefix := range []string{PrefixProject, PrefixItem, PrefixProfile, PrefixTemplate, PrefixStack, PrefixView, PrefixRun} {
		re := regexp.MustCompile("^" + prefix + "[0-9a-z]{10}$")
		seen := map[string]bool{}
		for i := 0; i < 500; i++ {
			id := NewID(prefix)
			if !re.MatchString(id) || !ValidID(prefix, id) {
				t.Fatalf("bad id %q", id)
			}
			if seen[id] {
				t.Fatalf("duplicate id %q", id)
			}
			seen[id] = true
		}
	}
}

func TestValidID(t *testing.T) {
	tests := []struct {
		prefix, id string
		ok         bool
	}{
		{PrefixProfile, "ap_claude", true},
		{PrefixItem, "i_7f3k2a9q0b", true},
		{PrefixItem, "p_7f3k2a9q0b", false},
		{PrefixItem, "i_", false},
		{PrefixItem, "i_ABC", false},
		{PrefixItem, "i_a b", false},
	}
	for _, tt := range tests {
		if got := ValidID(tt.prefix, tt.id); got != tt.ok {
			t.Errorf("ValidID(%q, %q) = %v", tt.prefix, tt.id, got)
		}
	}
}

func TestDeriveAlias(t *testing.T) {
	tests := map[string]string{
		"Shop backlog board":      "shop-backlog-board",
		"  --Hello__World--  ":    "hello-world",
		"API (staging) v2":        "api-staging-v2",
		"تجربة":                   "item",
		"":                        "item",
		strings.Repeat("ab ", 30): "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab",
	}
	for in, want := range tests {
		got := DeriveAlias(in)
		if got != want || !ValidAlias(got) {
			t.Errorf("DeriveAlias(%q) = %q want %q", in, got, want)
		}
	}
}

func TestUniqueAlias(t *testing.T) {
	taken := map[string]bool{"shop": true, "shop-2": true}
	if got := UniqueAlias("shop", func(a string) bool { return taken[a] }); got != "shop-3" {
		t.Fatalf("got %q", got)
	}
	if got := UniqueAlias("free", func(a string) bool { return taken[a] }); got != "free" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("a", MaxAliasLen)
	got := UniqueAlias(long, func(a string) bool { return a == long })
	if len(got) > MaxAliasLen || !strings.HasSuffix(got, "-2") || !ValidAlias(got) {
		t.Fatalf("long alias not shortened: %q", got)
	}
}

func TestCheckAlias(t *testing.T) {
	for alias, ok := range map[string]bool{
		"shop-backlog": true, "a": true, "9lives": true,
		"": false, "-x": false, "Shop": false, "a_b": false, "a b": false, strings.Repeat("a", 49): false,
	} {
		err := CheckAlias(alias)
		if (err == nil) != ok {
			t.Errorf("CheckAlias(%q) = %v", alias, err)
		}
		if err != nil && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "alias")) {
			t.Errorf("CheckAlias(%q) error not clear: %v", alias, err)
		}
	}
}

func TestPlaceholders(t *testing.T) {
	tests := []struct {
		in      string
		unknown []string
	}{
		{"backlog browser --port {port} --no-open", nil},
		{"cd {projectDir} && echo {projectName} {alias} {itemId} {home} {sessionId} {newSessionId} {name}", nil},
		{"Get-ChildItem | ForEach-Object { $_.Name }", nil}, // script blocks are not placeholders
		{`node -e "console.log({a:1})"`, nil},
		{"run {nope} {port} {nope} {Other}", []string{"nope", "Other"}},
	}
	for _, tt := range tests {
		got := UnknownPlaceholders(tt.in)
		if strings.Join(got, ",") != strings.Join(tt.unknown, ",") {
			t.Errorf("UnknownPlaceholders(%q) = %v want %v", tt.in, got, tt.unknown)
		}
		err := CheckPlaceholders("command.line", tt.in)
		if (err != nil) != (len(tt.unknown) > 0) {
			t.Errorf("CheckPlaceholders(%q) = %v", tt.in, err)
		}
		if err != nil && (!errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "command.line: unknown placeholder {nope}")) {
			t.Errorf("unclear error: %v", err)
		}
	}
	vals := map[string]string{"port": "6421", "alias": "shop"}
	if got := Expand("--port {port} {alias} {projectDir} { $_ }", vals); got != "--port 6421 shop {projectDir} { $_ }" {
		t.Fatalf("Expand = %q", got)
	}
	if got := ExpandAll([]string{"{port}", "x"}, vals); got[0] != "6421" || got[1] != "x" {
		t.Fatalf("ExpandAll = %v", got)
	}
}

func TestIconRefs(t *testing.T) {
	tests := []struct {
		ref        string
		forProject bool
		ok         bool
	}{
		{"builtin:kanban", false, true},
		{"emoji:🚀", false, true},
		{"file:3f2a9c1b7d0e.png", false, true},
		{"text:Cl:#d97757", false, true},
		{"text:ABC:#d97757", false, false},
		{"project", false, true},
		{"agent", false, true},
		{"favicon", false, true},
		{"auto", false, false},
		{"auto", true, true},
		{"project", true, false},
		{"builtin:Bad Name", false, false},
		{"file:../x.png", false, false},
		{"http://x/y.png", false, false},
	}
	for _, tt := range tests {
		if got := ValidIconRef(tt.ref, tt.forProject); got != tt.ok {
			t.Errorf("ValidIconRef(%q, %v) = %v", tt.ref, tt.forProject, got)
		}
	}
	for _, k := range Kinds {
		var c collector
		c.icon("icon", DefaultIcon(k), false)
		if c.err() != nil {
			t.Errorf("default icon for %s invalid: %v", k, c.err())
		}
	}
}
