package domain

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestValidateVariants(t *testing.T) {
	tests := []struct {
		name    string
		vs      []Variant
		wantErr string
	}{
		{"none", nil, ""},
		{"free-form labels and any flags", []Variant{
			{ID: "remote", Label: "Remote app-server ✓ (any text)", AppendArgs: []string{"--remote", "ws://127.0.0.1:4810"}},
			{ID: "bypass", Label: "Full access", AppendArgs: []string{"--dangerously-bypass-approvals-and-sandbox"}},
			{ID: "alt", Label: "Alt command", Line: "backlog browser --port {port}"},
			{ID: "exact", Argv: []string{"tool", "--x"}, PrependArgs: []string{"-v"}, Env: map[string]string{"FOO": "bar"}},
		}, ""},
		{"duplicate id", []Variant{{ID: "a"}, {ID: "a"}}, `variants[1].id: duplicate variant id "a"`},
		{"bad id", []Variant{{ID: "Has Space"}}, `variants[0].id: "Has Space" must be 1-24`},
		{"too long id", []Variant{{ID: strings.Repeat("a", 25)}}, "must be 1-24"},
		{"line and argv", []Variant{{ID: "x", Line: "a", Argv: []string{"b"}}}, "set either line or argv"},
		{"empty argv", []Variant{{ID: "x", Argv: []string{}}}, "executable must not be empty"},
		{"unknown placeholder", []Variant{{ID: "x", AppendArgs: []string{"{bogus}"}}}, "variants[0].appendArgs: unknown placeholder {bogus}"},
		{"bad env name", []Variant{{ID: "x", Env: map[string]string{"A-B": "1"}}}, `"A-B" is not a valid environment variable name`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVariants(tt.vs)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want ErrInvalid containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestVariantCountIsUnlimited(t *testing.T) {
	vs := make([]Variant, 500)
	for i := range vs {
		vs[i] = Variant{ID: fmt.Sprintf("v%d", i), Label: fmt.Sprintf("Variant number %d", i)}
	}
	if err := ValidateVariants(vs); err != nil {
		t.Fatal(err)
	}
}

func ids(vs []Variant) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID + "=" + v.Label
	}
	return strings.Join(out, ",")
}

func TestMergeVariants(t *testing.T) {
	profile := []Variant{{ID: "default", Label: "P default"}, {ID: "remote", Label: "P remote"}, {ID: "fast", Label: "P fast"}}
	item := []Variant{{ID: "remote", Label: "I remote"}, {ID: "mine", Label: "I mine"}}
	tests := []struct {
		name          string
		profile, item []Variant
		want          string
	}{
		{"item wins by id, order kept, item-only appended", profile, item, "default=P default,remote=I remote,fast=P fast,mine=I mine"},
		{"implicit default added first", nil, []Variant{{ID: "x", Label: "X"}}, "default=Default,x=X"},
		{"item may redefine default", nil, []Variant{{ID: "default", Label: "Mine"}}, "default=Mine"},
		{"empty", nil, nil, "default=Default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(MergeVariants(tt.profile, tt.item)); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestResolveVariantOrder(t *testing.T) {
	merged := MergeVariants([]Variant{{ID: "p1"}, {ID: "p2"}}, []Variant{{ID: "i1"}})
	tests := []struct {
		name                            string
		chosen, itemDefault, profileDef string
		want                            string
		notFound                        bool
	}{
		{"chosen wins", "p2", "i1", "p1", "p2", false},
		{"item default next", "", "i1", "p1", "i1", false},
		{"profile default next", "", "", "p1", "p1", false},
		{"implicit default last", "", "", "", "default", false},
		{"unknown chosen", "nope", "i1", "p1", "", true},
		{"unknown item default", "", "gone", "p1", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ResolveVariant(merged, tt.chosen, tt.itemDefault, tt.profileDef)
			if tt.notFound {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("want ErrNotFound, got %v %v", v, err)
				}
				return
			}
			if err != nil || v.ID != tt.want {
				t.Fatalf("got %q %v want %q", v.ID, err, tt.want)
			}
		})
	}
	if v, err := ResolveVariant(nil, "", "", ""); err != nil || v.ID != DefaultVariantID {
		t.Fatalf("implicit default without list: %v %v", v, err)
	}
}

func TestVariantApply(t *testing.T) {
	argvBase := Spec{Argv: []string{"codex", "resume", "{sessionId}"}, Env: map[string]string{"A": "1"}}
	lineBase := Spec{Line: "backlog browser --port {port}"}
	tests := []struct {
		name string
		base Spec
		v    Variant
		want Spec
	}{
		{"default leaves argv alone", argvBase, Variant{ID: "default"}, argvBase},
		{"append and prepend on argv", argvBase,
			Variant{ID: "x", AppendArgs: []string{"--remote", "ws://127.0.0.1:4810"}, PrependArgs: []string{"-v"}, Env: map[string]string{"A": "2", "B": "3"}},
			Spec{Argv: []string{"codex", "-v", "resume", "{sessionId}", "--remote", "ws://127.0.0.1:4810"}, Env: map[string]string{"A": "2", "B": "3"}}},
		{"argv replaces the command", lineBase, Variant{ID: "x", Argv: []string{"tool", "a"}, AppendArgs: []string{"z"}},
			Spec{Argv: []string{"tool", "a", "z"}}},
		{"line replaces the command", argvBase, Variant{ID: "x", Line: "other {port}"},
			Spec{Line: "other {port}", Env: map[string]string{"A": "1"}}},
		{"args on a line stay apart for shell quoting", lineBase, Variant{ID: "x", AppendArgs: []string{"--no-open"}, PrependArgs: []string{"-q"}},
			Spec{Line: "backlog browser --port {port}", AppendArgs: []string{"--no-open"}, PrependArgs: []string{"-q"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.Apply(tt.base)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if argvBase.Env["A"] != "1" || len(argvBase.Argv) != 3 {
		t.Fatal("Apply modified its input")
	}
}
