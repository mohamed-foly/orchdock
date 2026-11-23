package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

func TestExportRedactsAndRoundTrips(t *testing.T) {
	home := t.TempDir()
	store := catalog.New(home, nil)
	if err := os.MkdirAll(filepath.Join(home, IconDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, IconDir, "shop.png"), []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := store.Update(func(f *catalog.File) error {
		mcp := domain.NewKind("MCP server", domain.KindService, "#db2777")
		f.Kinds = append(f.Kinds, mcp)
		f.SetTagDef(domain.TagDef{Name: "shop", Path: "/work/shop", Icon: domain.Icon{Primary: "file:shop.png"}})
		it := f.NewItem(domain.KindService, "Images", "images", now)
		it.KindID = mcp.ID
		it.Tags = []string{"shop"}
		it.Command.Line = "node server.js --port {port}"
		it.Command.Env = map[string]string{"API_TOKEN": "s3cret-value"}
		it.Command.Variants = []domain.Variant{{ID: "debug", Env: map[string]string{"DEBUG_KEY": "another-secret"}}}
		it.Service.Port, it.Service.PortMode = 7450, "fixed"
		loose := f.NewItem(domain.KindTask, "Loose", "loose", now)
		loose.Command.Line = "echo hi"
		f.Items = append(f.Items, it, loose)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cat, _ := store.Load()

	b, err := Export(cat, home, false, now)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, secret := range []string{"s3cret-value", "another-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("an environment value was exported: %s", secret)
		}
	}
	if !strings.Contains(text, "API_TOKEN") || !strings.Contains(text, `"envRedacted": true`) {
		t.Fatal("variable names stay, and the file says the values were left out")
	}
	if withEnv, _ := Export(cat, home, true, now); !strings.Contains(string(withEnv), "s3cret-value") {
		t.Fatal("includeEnv exports the values")
	}

	// import into another home
	other := catalog.New(t.TempDir(), nil)
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(other, f, false, now)
	if err != nil || len(rep.Created) != 2 || len(rep.Rejected) != 0 || len(rep.Icons) != 1 {
		t.Fatalf("import: %+v %v", rep, err)
	}
	if rep.Commands["images"] != "node server.js --port {port}" {
		t.Fatalf("command preview: %v", rep.Commands)
	}
	got, _ := other.Load()
	byAlias := map[string]domain.Item{}
	for _, it := range got.Items {
		byAlias[it.Alias] = it
	}
	img := byAlias["images"]
	k, _ := got.FindKind("MCP server")
	d, _ := got.TagDefOf("shop")
	if img.KindID != k.ID || img.Tags[0] != "shop" || img.Service.Port != 7450 || img.Command.Env["API_TOKEN"] != "" || d.Icon.Primary != "file:shop.png" || d.Path != "/work/shop" {
		t.Fatalf("round trip: %+v (kind %s, tag %+v)", img, k.ID, d)
	}
	if len(byAlias["loose"].Tags) != 0 {
		t.Fatal("an item without tags stays without them")
	}
	if _, err := os.Stat(filepath.Join(other.Home(), IconDir, "shop.png")); err != nil {
		t.Fatalf("embedded icon not written: %v", err)
	}
	// importing again merges: nothing new
	again, _ := Run(other, f, false, now)
	if len(again.Created) != 0 || len(again.Skipped) != 2 {
		t.Fatalf("second import: %+v", again)
	}
}
