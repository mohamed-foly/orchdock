package domain

import (
	"errors"
	"strings"
	"testing"
)

func kindCatalog(t *testing.T) (Catalog, KindDef) {
	t.Helper()
	cat := NewCatalog()
	mcp := NewKind("MCP server", KindService, "#db2777")
	cat.Kinds = append(cat.Kinds, mcp, KindDef{ID: "task", Name: "Jobs", Base: KindTask, Color: "#0e7490"})
	it := cat.NewItem(KindService, "Images", "images", now)
	it.Command.Line = "node dist/index.js"
	it.KindID = mcp.ID
	cat.Items = append(cat.Items, it)
	if err := cat.Validate(); err != nil {
		t.Fatalf("valid catalog: %v", err)
	}
	return cat, mcp
}

func TestUserKinds(t *testing.T) {
	cat, mcp := kindCatalog(t)
	all := cat.AllKinds()
	if len(all) != len(Kinds)+1 || all[len(all)-1].ID != mcp.ID {
		t.Fatalf("AllKinds: %+v", all)
	}
	if k, err := cat.FindKind("mcp SERVER"); err != nil || k.ID != mcp.ID {
		t.Fatalf("by name: %v %v", k, err)
	}
	if k, err := cat.FindKind("jobs"); err != nil || k.ID != "task" {
		t.Fatalf("renamed built-in: %v %v", k, err)
	}
	if k, err := cat.FindKind("services"); err != nil || k.ID != "service" {
		t.Fatalf("built-in by plural: %v %v", k, err)
	}
	if !cat.KindNameTaken("Jobs", "") || cat.KindNameTaken("Jobs", "task") || !cat.KindNameTaken("agent", "") {
		t.Fatal("name check")
	}
}

func TestKindValidation(t *testing.T) {
	cases := map[string]func(*Catalog){
		"is already the name":     func(c *Catalog) { c.Kinds = append(c.Kinds, NewKind("mcp server", KindTask, "")) },
		"keeps its own behaviour": func(c *Catalog) { c.Kinds[1].Base = KindShell },
		"is not one of":           func(c *Catalog) { c.Kinds[0].Base = "robot" },
		"is not a colour":         func(c *Catalog) { c.Kinds[0].Color = "pink" },
		"is based on service": func(c *Catalog) {
			c.Items[0].Kind, c.Items[0].Service = KindTask, nil
			c.Items[0].Task = &Task{Missed: "skip", Overlap: "skip"}
		},
		`kind "k_missing" does not`: func(c *Catalog) { c.Items[0].KindID = "k_missing" },
		`kind "service" does not`:   func(c *Catalog) { c.Items[0].KindID = "service" },
	}
	for want, mut := range cases {
		cat, _ := kindCatalog(t)
		mut(&cat)
		if err := cat.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestRemoveKind(t *testing.T) {
	cat, mcp := kindCatalog(t)
	if err := cat.RemoveKind("service", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("built-in: %v", err)
	}
	if err := cat.RemoveKind(mcp.ID, "task"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("moving service items to a task kind: %v", err)
	}
	other := NewKind("Web app", KindService, "")
	cat.Kinds = append(cat.Kinds, other)
	if err := cat.RemoveKind(mcp.ID, other.ID); err != nil || cat.Items[0].KindID != other.ID {
		t.Fatalf("move to another kind: %v %q", err, cat.Items[0].KindID)
	}
	if err := cat.RemoveKind(other.ID, "service"); err != nil || cat.Items[0].KindID != "" {
		t.Fatalf("move to the plain base: %v %q", err, cat.Items[0].KindID)
	}
	if err := cat.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := cat.RemoveKind("k_gone", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestMoveKind(t *testing.T) {
	cat := NewCatalog()
	a, b, c := NewKind("A", KindTask, ""), NewKind("B", KindTask, ""), NewKind("C", KindTask, "")
	cat.Kinds = append(cat.Kinds, a, KindDef{ID: "task", Name: "Jobs", Base: KindTask}, b, c)
	order := func() string {
		var s []string
		for _, k := range cat.AllKinds()[len(Kinds):] {
			s = append(s, k.Name)
		}
		return strings.Join(s, ",")
	}
	steps := []struct {
		id   string
		by   int
		want string
	}{
		{c.ID, -1, "A,C,B"}, {c.ID, -1, "C,A,B"}, {c.ID, -1, "C,A,B"}, {a.ID, 1, "C,B,A"}, {a.ID, 5, "C,B,A"}, {c.ID, 2, "B,A,C"},
	}
	for _, s := range steps {
		if err := cat.MoveKind(s.id, s.by); err != nil {
			t.Fatal(err)
		}
		if got := order(); got != s.want {
			t.Fatalf("after moving by %d: %s, want %s", s.by, got, s.want)
		}
	}
	if err := cat.MoveKind("task", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("built-in: %v", err)
	}
	if cat.AllKinds()[2].Name != "Jobs" {
		t.Fatal("the built-in entry must survive moves")
	}
}
