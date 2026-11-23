package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

var now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// sample builds an import file with invented names. root is a folder the
// "shop" project owns; img and bad are image files.
func sample(root, img, badSVG string) string {
	j := func(s string) string { return strings.ReplaceAll(s, `\`, `/`) }
	return fmt.Sprintf(`{
  "note": "unknown top-level keys are ignored",
  "iconFiles": {"shop.png": %q, "evil.svg": %q, "gone.png": %q, "Bad Name.png": %q},
  "projects": [
    {"name": "shop", "path": %q, "icon": {"primary": "file:shop.png"}},
    {"name": "scratch", "path": "", "icon": {"primary": "file:evil.svg"}},
    {"name": "", "path": "/nowhere"}
  ],
  "items": [
    {"alias": "shop-claude", "title": "Claude", "kind": "agent", "project": "shop", "tags": ["claude"],
     "icon": {"primary": "project", "badges": ["file:shop.png", "file:gone.png"]},
     "command": {"line": "claude", "cwd": %q, "variants": [{"id": "continue", "label": "Continue", "appendArgs": ["-c"]}]}},
    {"alias": "shop-board", "title": "Board", "kind": "service",
     "command": {"line": "board --port {port}", "cwd": %q}, "service": {"port": 6500}},
    {"alias": "notify", "title": "Notify", "kind": "service", "project": "scratch",
     "command": {"shell": "none", "argv": ["node", "server.js"]}, "service": {"port": 7450, "portMode": "auto"}},
    {"alias": "loose", "title": "Loose task", "kind": "task", "command": {"line": "echo hi"}},
    {"alias": "existing", "title": "Again", "kind": "task", "command": {"line": "echo again"}},
    {"alias": "no-cmd", "title": "Broken", "kind": "task"},
    {"alias": "lost", "title": "Lost", "kind": "task", "project": "nope", "command": {"line": "x"}},
    {"title": "", "kind": "task"},
    "not an object"
  ]
}`, j(img), j(badSVG), j(filepath.Join(root, "missing.png")), j(img), j(root),
		j(filepath.Join(root, "app")), j(root))
}

func setup(t *testing.T) (*catalog.Store, string, string, string) {
	t.Helper()
	home, root, files := t.TempDir(), t.TempDir(), t.TempDir()
	img := filepath.Join(files, "logo.png")
	bad := filepath.Join(files, "evil.svg")
	if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`<svg onload="alert(1)"></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := catalog.New(home, nil)
	err := store.Update(func(f *catalog.File) error {
		it := f.NewItem(domain.KindTask, "Existing", "existing", now)
		it.Command.Line = "echo"
		f.Items = append(f.Items, it)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, root, img, bad
}

func TestImport(t *testing.T) {
	store, root, img, bad := setup(t)
	f, err := Parse([]byte(sample(root, img, bad)))
	if err != nil {
		t.Fatal(err)
	}

	dry, err := Run(store, f, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if cat, _ := store.Load(); len(cat.Items) != 1 || len(cat.Tags) != 0 {
		t.Fatalf("a dry run changed the catalog: %d items, %d tags", len(cat.Items), len(cat.Tags))
	}
	if _, err := os.Stat(filepath.Join(store.Home(), IconDir, "shop.png")); err == nil {
		t.Fatal("a dry run copied an icon")
	}

	rep, err := Run(store, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dry.Created, rep.Created) {
		t.Fatalf("dry run and real run differ: %v vs %v", dry.Created, rep.Created)
	}
	if want := []string{"shop-claude", "shop-board", "notify", "loose", "lost"}; !slices.Equal(rep.Created, want) {
		t.Fatalf("created %v, want %v", rep.Created, want)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Name != "existing" {
		t.Fatalf("skipped %+v", rep.Skipped)
	}
	rejected := map[string]string{}
	for _, e := range rep.Rejected {
		rejected[e.Name] = e.Reason
	}
	for _, name := range []string{"no-cmd", "items[7]", "items[8]", "(project without a name)"} {
		if rejected[name] == "" {
			t.Errorf("%s should be rejected; rejected: %v", name, rejected)
		}
	}
	if !slices.Equal(rep.TagsCreated, []string{"shop", "scratch"}) || !slices.Equal(rep.Icons, []string{"shop.png"}) {
		t.Fatalf("tags %v icons %v", rep.TagsCreated, rep.Icons)
	}
	problems := fmt.Sprint(rep.IconProblems)
	for _, want := range []string{"Bad Name.png", "evil.svg", "gone.png", "scripts"} {
		if !strings.Contains(problems, want) {
			t.Errorf("icon problems should mention %q: %s", want, problems)
		}
	}

	cat, _ := store.Load()
	shop, _ := cat.TagDefOf("shop")
	scratch, _ := cat.TagDefOf("scratch")
	if shop.Icon.Primary != "file:shop.png" || scratch.Icon.Primary != "" || shop.Path == "" {
		t.Fatalf("tags from projects: %+v %+v", shop, scratch)
	}
	if len(cat.Views) != 2 || cat.Views[0].Query != "tag:shop" {
		t.Fatalf("a project in an older file also becomes a section: %+v", cat.Views)
	}
	got := map[string]domain.Item{}
	for _, it := range cat.Items {
		got[it.Alias] = it
	}
	c := got["shop-claude"]
	if !slices.Equal(c.Tags, []string{"shop", "claude"}) || len(c.Command.Variants) != 1 || c.Kind != domain.KindAgent {
		t.Fatalf("shop-claude: %+v", c)
	}
	if !slices.Equal(c.Icon.Badges, []string{"file:shop.png"}) {
		t.Fatalf("a missing badge image should be dropped: %+v", c.Icon)
	}
	if b := got["shop-board"]; len(b.Tags) != 0 || b.Service.Port != 6500 || b.Service.PortMode != "fixed" {
		t.Fatalf("shop-board keeps a fixed port and is not tagged by folder (that is only suggested): %+v %+v", b.Tags, b.Service)
	}
	if sug := cat.SuggestTags(got["shop-board"].Command.Cwd); !slices.Equal(sug, []string{"shop"}) {
		t.Fatalf("its folder suggests the tag: %v", sug)
	}
	if n := got["notify"]; !slices.Equal(n.Tags, []string{"scratch"}) || n.Service.PortMode != "auto" {
		t.Fatalf("notify: %+v", n)
	}
	if len(got["loose"].Tags) != 0 || !slices.Equal(got["lost"].Tags, []string{"nope"}) {
		t.Fatalf("loose has no tag, lost gets its unknown project as a tag: %v %v", got["loose"].Tags, got["lost"].Tags)
	}
	if _, err := os.Stat(filepath.Join(store.Home(), IconDir, "shop.png")); err != nil {
		t.Fatalf("icon not copied: %v", err)
	}

	again, err := Run(store, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Created) != 0 || len(again.TagsCreated) != 0 || len(again.TagsReused) != 2 {
		t.Fatalf("importing twice should create nothing: %+v", again)
	}
}

func TestParseRefusesNonsense(t *testing.T) {
	for _, in := range []string{`[]`, `{}`, `not json`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
	if _, err := Parse([]byte("\xEF\xBB\xBF" + `{"items":[{}]}`)); err != nil {
		t.Errorf("a byte order mark should be ignored: %v", err)
	}
}
