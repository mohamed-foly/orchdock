package domain

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestTagSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Corner Shop": "corner-shop", "shop": "shop", "  Temp  ": "temp", "C++ / Go!": "c-go",
		"": "tag", "a.b_c": "a.b_c", "this name is far too long to be a tag at all": "this-name-is-far-too-long-to-be",
	} {
		if got := TagSlug(in); got != want || !ValidTag(got) {
			t.Errorf("TagSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggestTagsDeepestFirst(t *testing.T) {
	cat := NewCatalog()
	cat.SetTagDef(TagDef{Name: "shop", Path: "/work/shop"})
	cat.SetTagDef(TagDef{Name: "shop-api", Path: "/work/shop/api"})
	cat.SetTagDef(TagDef{Name: "temp"}) // no folder: never suggested
	cases := map[string][]string{
		"/work/shop/api/src": {"shop-api", "shop"},
		`\work\shop\web`:     {"shop"},
		"/work/shopping":     nil,
		"{projectDir}/x":     nil,
		"":                   nil,
	}
	for dir, want := range cases {
		if got := cat.SuggestTags(dir); !slices.Equal(got, want) {
			t.Errorf("SuggestTags(%q) = %v, want %v", dir, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		if got := cat.SuggestTags("/WORK/Shop"); !slices.Equal(got, []string{"shop"}) {
			t.Errorf("case should not matter on Windows: %v", got)
		}
	}
}

func TestHomeOfIsTheFirstTagWithAFolder(t *testing.T) {
	cat := NewCatalog()
	cat.SetTagDef(TagDef{Name: "daily"})
	cat.SetTagDef(TagDef{Name: "shop", Path: "/work/shop"})
	cat.SetTagDef(TagDef{Name: "lab", Path: "/work/lab"})
	if h := cat.HomeOf(Item{Tags: []string{"daily", "shop", "lab"}}); h == nil || h.Name != "shop" {
		t.Fatalf("home: %+v", h)
	}
	if h := cat.HomeOf(Item{Tags: []string{"daily", "unknown"}}); h != nil {
		t.Fatalf("no folder tag: %+v", h)
	}
	if p := ProjectOf(cat.HomeOf(Item{Tags: []string{"lab"}})); p == nil || p.Path != "/work/lab" || p.Name != "lab" {
		t.Fatalf("project shape: %+v", p)
	}
}

func TestMigrateProjects(t *testing.T) {
	cat := Catalog{SchemaVersion: SchemaVersion}
	p := NewProject("Corner Shop", "/work/corner-shop", now)
	p.Color = "#4f8cff"
	p.Icon = Icon{Primary: "file:corner.svg"}
	temp := NewProject("temp", "", now)
	cat.Projects = []Project{p, temp}
	it := NewCatalog().NewItem(KindTask, "Pull", "pull", now)
	it.Command.Line = "git pull"
	it.ProjectID = p.ID
	it.Tags = []string{"daily"}
	it.Task.Targets = []string{p.ID}
	gone := NewCatalog().NewItem(KindShell, "Old", "old", now)
	gone.ProjectID = temp.ID
	cat.Items = []Item{it}
	cat.Trash = []TrashedItem{{Item: gone, DeletedAt: now}}
	cat.TagColors = map[string]string{"daily": "#8b5cf6"}

	cat.Normalize() // migrates
	if len(cat.Projects) != 0 || cat.TagColors != nil {
		t.Fatalf("old fields left: %+v %+v", cat.Projects, cat.TagColors)
	}
	d, ok := cat.TagDefOf("corner-shop")
	if !ok || d.Path != "/work/corner-shop" || d.Color != "#4f8cff" || d.Icon.Primary != "file:corner.svg" {
		t.Fatalf("tag from project: %+v", d)
	}
	if d, ok := cat.TagDefOf("daily"); !ok || d.Color != "#8b5cf6" {
		t.Fatalf("tag colour: %+v", d)
	}
	got := cat.Items[0]
	if got.ProjectID != "" || !slices.Equal(got.Tags, []string{"corner-shop", "daily"}) || !slices.Equal(got.Task.Targets, []string{"corner-shop"}) {
		t.Fatalf("item: %+v targets %v", got.Tags, got.Task.Targets)
	}
	if !slices.Equal(cat.Trash[0].Item.Tags, []string{"temp"}) {
		t.Fatalf("trashed item: %+v", cat.Trash[0].Item.Tags)
	}
	var sections []string
	for _, v := range cat.Views {
		sections = append(sections, v.Name+"="+v.Query)
	}
	if !slices.Equal(sections, []string{"Corner Shop=tag:corner-shop", "temp=tag:temp"}) {
		t.Fatalf("sections: %v", sections)
	}
	if err := cat.Validate(); err != nil {
		t.Fatalf("migrated catalog: %v", err)
	}
	before := len(cat.Views)
	cat.Normalize() // a second load changes nothing
	if len(cat.Views) != before {
		t.Fatal("migration must run once")
	}
}

func TestRenameAndRemoveTag(t *testing.T) {
	cat := NewCatalog()
	cat.SetTagDef(TagDef{Name: "shop", Path: "/work/shop"})
	it := cat.NewItem(KindTask, "Pull", "pull", now)
	it.Command.Line = "git pull"
	it.Tags = []string{"shop", "daily"}
	it.Task.Targets = []string{"shop"}
	cat.Items = append(cat.Items, it)
	cat.Views = append(cat.Views, View{ID: "v_shop", Name: "Shop", Query: "tag:shop is:failed"})

	if err := cat.RenameTag("shop", "store"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cat.TagDefOf("store"); !ok || !slices.Equal(cat.Items[0].Tags, []string{"store", "daily"}) || cat.Items[0].Task.Targets[0] != "store" || cat.Views[0].Query != "tag:store is:failed" {
		t.Fatalf("rename: %+v %+v %q", cat.Tags, cat.Items[0].Tags, cat.Views[0].Query)
	}
	if err := cat.RenameTag("store", "Not A Tag"); err == nil {
		t.Fatal("an invalid new name must be refused")
	}
	cat.RemoveTagDef("store", false)
	if _, ok := cat.TagDefOf("store"); ok || !slices.Contains(cat.Items[0].Tags, "store") {
		t.Fatal("removing the definition keeps the tag on items")
	}
	cat.SetTagDef(TagDef{Name: "store"})
	cat.RemoveTagDef("store", true)
	if slices.Contains(cat.Items[0].Tags, "store") || len(cat.Items[0].Task.Targets) != 0 {
		t.Fatalf("removing from items: %+v", cat.Items[0])
	}
}

func TestTagDefValidation(t *testing.T) {
	for want, d := range map[string]TagDef{
		"must be 1-32":        {Name: "Bad Name"},
		"absolute folder":     {Name: "x", Path: "relative/dir"},
		"is not a colour":     {Name: "x", Color: "red"},
		"is not a valid icon": {Name: "x", Icon: Icon{Primary: "project"}},
	} {
		cat := NewCatalog()
		cat.Tags = []TagDef{d}
		if err := cat.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestAgentIsACommand(t *testing.T) {
	it := NewCatalog().NewItem(KindAgent, "Claude", "", now)
	it.Command.Line = "claude --continue"
	if err := it.Validate(); err != nil {
		t.Fatalf("agent with its own command: %v", err)
	}
}
