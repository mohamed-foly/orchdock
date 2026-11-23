// Package importer fills the catalog from a JSON file: projects, items and
// their icon images. One bad entry never stops the good ones.
package importer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

// MaxIconBytes is the largest icon image accepted.
const MaxIconBytes = 512 << 10

// IconDir is where icon images live, inside the home folder.
const IconDir = "icons"

// File is the import format.
//
//	{ "tags":      [{"name": "shop", "path": "/work/shop", "color": "#4f8cff", "icon": {"primary": "file:shop.png"}}],
//	  "iconFiles": {"shop.png": "/path/to/logo.png"},
//	  "items":     [{"alias": "shop-dev", "title": "Dev server", "kind": "service", "tags": ["shop"],
//	                 "command": {"line": "npm run dev -- --port {port}", "cwd": "/work/shop"}, "service": {"port": 5173}}] }
//
// Items use the catalog's own item fields. Tags are listed only to give them
// a folder, colour or icon. Older files with "projects" and an item
// "project" still import: each project becomes a tag (with a section), and
// the item gets that tag first. A service port given without a portMode is
// kept as a fixed port. Nothing is filed by folder: that is a suggestion in
// the window.
type File struct {
	Kinds     []Kind            `json:"kinds,omitempty"`     // user kinds, created when missing (by name)
	Tags      []domain.TagDef   `json:"tags,omitempty"`      // tag folders, colours and icons
	Projects  []Project         `json:"projects,omitempty"`  // older files: become tags
	IconFiles map[string]string `json:"iconFiles,omitempty"` // name -> image file on this machine
	IconData  map[string]string `json:"iconData,omitempty"`  // name -> image as base64 (what export writes)
	Items     []json.RawMessage `json:"items"`
}

// Kind is a user kind in the import file.
type Kind struct {
	Name  string      `json:"name"`
	Base  domain.Kind `json:"base"`
	Color string      `json:"color,omitempty"`
}

// Project is a project in an older import file; it becomes a tag.
type Project struct {
	Name  string      `json:"name"`
	Path  string      `json:"path"`
	Color string      `json:"color"`
	Icon  domain.Icon `json:"icon"`
}

// Entry names something and says why it was not taken as is.
type Entry struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Report says what an import did (or would do, on a dry run).
type Report struct {
	DryRun       bool     `json:"dryRun"`
	Created      []string `json:"created"`
	Skipped      []Entry  `json:"skipped"`
	Rejected     []Entry  `json:"rejected"`
	TagsCreated  []string `json:"tagsCreated"`
	TagsReused   []string `json:"tagsReused"`
	Icons        []string `json:"icons"`
	IconProblems []Entry  `json:"iconProblems"`
	// Commands shows what each created item runs, so a preview can be checked before importing.
	Commands map[string]string `json:"commands"`
}

// Parse reads an import file.
func Parse(data []byte) (File, error) {
	var f File
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("%w: not an import file: %v", domain.ErrInvalid, err)
	}
	if len(f.Items) == 0 && len(f.Projects) == 0 && len(f.Tags) == 0 {
		return File{}, fmt.Errorf("%w: the file has no tags and no items", domain.ErrInvalid)
	}
	return f, nil
}

// Run imports f into the catalog of store. With dryRun nothing is written.
func Run(store *catalog.Store, f File, dryRun bool, now time.Time) (Report, error) {
	r := Report{DryRun: dryRun, Commands: map[string]string{}}
	icons := r.copyIcons(filepath.Join(store.Home(), IconDir), f.IconFiles, dryRun)
	r.writeIconData(filepath.Join(store.Home(), IconDir), f.IconData, icons, dryRun)
	apply := func(cat *catalog.File) error {
		r.kinds(cat, f.Kinds)
		r.tags(cat, f.Tags, icons)
		r.projects(cat, f.Projects, icons)
		for i, raw := range f.Items {
			r.item(cat, i, raw, icons, now)
		}
		return nil
	}
	if dryRun {
		cat, err := store.Load()
		if err != nil {
			return r, err
		}
		return r, apply(&cat)
	}
	return r, store.Update(apply)
}

// --- icons --------------------------------------------------------------------

var (
	unsafeSVG = regexp.MustCompile(`(?i)<script|<foreignobject|javascript:|\son[a-z]+\s*=|<iframe|<embed|<object`)
	iconExt   = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".ico": true, ".svg": true}
)

// copyIcons checks every image and copies the good ones into dir. It
// returns the names an item may now refer to.
func (r *Report) copyIcons(dir string, files map[string]string, dryRun bool) map[string]bool {
	ok := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil { // icons already in the home can be used too
		for _, e := range entries {
			ok[e.Name()] = true
		}
	}
	for _, name := range sortedKeys(files) {
		if err := checkIcon(name, files[name]); err != nil {
			r.IconProblems = append(r.IconProblems, Entry{name, err.Error()})
			delete(ok, name)
			continue
		}
		if !dryRun {
			b, err := os.ReadFile(files[name])
			if err == nil {
				err = os.MkdirAll(dir, 0o700)
			}
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, name), b, 0o600)
			}
			if err != nil {
				r.IconProblems = append(r.IconProblems, Entry{name, err.Error()})
				continue
			}
		}
		ok[name] = true
		r.Icons = append(r.Icons, name)
	}
	return ok
}

func checkIcon(name, src string) error {
	if !domain.ValidIconRef("file:"+name, false) {
		return errors.New("the name must be lowercase letters, digits, '-' or '_' with .png, .jpg, .ico or .svg")
	}
	if !iconExt[strings.ToLower(filepath.Ext(src))] {
		return fmt.Errorf("%s is not a png, jpg, ico or svg image", src)
	}
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("cannot read %s", src)
	}
	if st.Size() > MaxIconBytes {
		return fmt.Errorf("%s is larger than %d KB", src, MaxIconBytes>>10)
	}
	if strings.EqualFold(filepath.Ext(src), ".svg") {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if unsafeSVG.Match(b) {
			return errors.New("the SVG contains scripts or event handlers")
		}
	}
	return nil
}

// keepIcons drops references to images that are not available; an item
// whose main image is gone falls back to the default icon of its kind.
func keepIcons(ic domain.Icon, ok map[string]bool, fallback domain.Icon) (domain.Icon, []string) {
	var lost []string
	usable := func(ref string) bool {
		if name, isFile := strings.CutPrefix(ref, "file:"); isFile && !ok[name] {
			lost = append(lost, name)
			return false
		}
		return true
	}
	if ic.Primary != "" && !usable(ic.Primary) {
		ic = fallback
	}
	var badges []string
	for _, b := range ic.Badges {
		if usable(b) {
			badges = append(badges, b)
		}
	}
	ic.Badges = badges
	return ic, lost
}

// --- tags -----------------------------------------------------------------------

// tags adds the tag definitions the catalog does not have yet.
func (r *Report) tags(cat *catalog.File, in []domain.TagDef, icons map[string]bool) {
	for _, d := range in {
		d.Name = strings.TrimSpace(d.Name)
		if _, exists := cat.TagDefOf(d.Name); exists {
			r.TagsReused = append(r.TagsReused, d.Name)
			continue
		}
		ic, lost := keepIcons(d.Icon, icons, domain.Icon{})
		d.Icon = ic
		r.lostIcons("tag "+d.Name, lost)
		if msg := checkTagDef(d); msg != "" {
			r.Rejected = append(r.Rejected, Entry{"tag " + d.Name, msg})
			continue
		}
		cat.SetTagDef(d)
		r.TagsCreated = append(r.TagsCreated, d.Name)
	}
}

// projects turns the projects of an older file into tags, each with a
// section (a saved search), as the catalog migration does.
func (r *Report) projects(cat *catalog.File, in []Project, icons map[string]bool) {
	for _, p := range in {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			r.Rejected = append(r.Rejected, Entry{"(project without a name)", "name is required"})
			continue
		}
		tag := domain.TagSlug(name)
		if _, exists := cat.TagDefOf(tag); exists {
			r.TagsReused = append(r.TagsReused, tag)
			continue
		}
		ic, lost := keepIcons(p.Icon, icons, domain.Icon{})
		r.lostIcons("tag "+tag, lost)
		d := domain.TagDef{Name: tag, Path: strings.TrimSpace(p.Path), Color: p.Color, Icon: ic}
		if msg := checkTagDef(d); msg != "" {
			r.Rejected = append(r.Rejected, Entry{"tag " + tag, msg})
			continue
		}
		cat.SetTagDef(d)
		q := "tag:" + tag
		if !slices.ContainsFunc(cat.Views, func(v domain.View) bool { return v.Query == q }) {
			cat.Views = append(cat.Views, domain.View{ID: domain.NewID(domain.PrefixView), Name: name, Query: q})
		}
		r.TagsCreated = append(r.TagsCreated, tag)
	}
}

// checkTagDef validates one definition on its own ("" when it is fine).
func checkTagDef(d domain.TagDef) string {
	probe := domain.NewCatalog()
	probe.Tags = []domain.TagDef{d}
	if err := probe.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

func (r *Report) lostIcons(owner string, lost []string) {
	for _, name := range lost {
		r.IconProblems = append(r.IconProblems, Entry{owner, fmt.Sprintf("icon %s is not available; using the default", name)})
	}
}

// --- items --------------------------------------------------------------------

func (r *Report) item(cat *catalog.File, i int, raw json.RawMessage, icons map[string]bool, now time.Time) {
	var head struct {
		Kind    domain.Kind `json:"kind"`
		Title   string      `json:"title"`
		Alias   string      `json:"alias"`
		Project *string     `json:"project"`
		Service *struct {
			PortMode *string `json:"portMode"`
		} `json:"service"`
	}
	label := fmt.Sprintf("items[%d]", i)
	if err := json.Unmarshal(raw, &head); err != nil {
		r.Rejected = append(r.Rejected, Entry{label, "not an item: " + err.Error()})
		return
	}
	if head.Alias != "" {
		label = head.Alias
	} else if head.Title != "" {
		label = head.Title
	}
	if head.Title == "" || head.Kind == "" {
		r.Rejected = append(r.Rejected, Entry{label, "kind and title are required"})
		return
	}
	if head.Alias != "" && cat.AliasTaken(head.Alias) {
		r.Skipped = append(r.Skipped, Entry{head.Alias, "an item with this alias exists"})
		return
	}
	base, kindID, err := cat.ResolveKind(string(head.Kind))
	if err != nil {
		r.Rejected = append(r.Rejected, Entry{label, err.Error()})
		return
	}
	it := cat.NewItem(base, head.Title, head.Alias, now)
	id, alias, defIcon := it.ID, it.Alias, it.Icon
	if err := json.Unmarshal(raw, &it); err != nil {
		r.Rejected = append(r.Rejected, Entry{label, err.Error()})
		return
	}
	it.ID, it.Alias, it.CreatedAt, it.UpdatedAt, it.LastRunAt = id, alias, now, now, time.Time{}
	it.Kind = base
	if kindID != "" {
		it.KindID = kindID
	}
	it.ProjectID = ""
	if head.Project != nil && strings.TrimSpace(*head.Project) != "" { // an older file: its project is now a tag
		tag := domain.TagSlug(*head.Project)
		if !slices.Contains(it.Tags, tag) {
			it.Tags = append([]string{tag}, it.Tags...)
		}
	}
	if it.Service != nil && it.Service.Port > 0 && (head.Service == nil || head.Service.PortMode == nil) {
		it.Service.PortMode = "fixed" // a port written in the file is meant to stay
	}
	ic, lost := keepIcons(it.Icon, icons, defIcon)
	it.Icon = ic
	r.lostIcons(alias, lost)
	if err := it.Validate(); err != nil {
		r.Rejected = append(r.Rejected, Entry{label, err.Error()})
		return
	}
	cat.Items = append(cat.Items, it)
	r.Created = append(r.Created, alias)
	if it.Command.Line != "" {
		r.Commands[alias] = it.Command.Line
	} else if len(it.Command.Argv) > 0 {
		r.Commands[alias] = strings.Join(it.Command.Argv, " ")
	} else if it.Link != nil {
		r.Commands[alias] = "opens " + it.Link.Target
	}
}

func sortedKeys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }

// Summary is a short human line for the report.
func (r Report) Summary() string {
	verb := "imported"
	if r.DryRun {
		verb = "would import"
	}
	return fmt.Sprintf("%s %d items (%d skipped, %d rejected), %d new tags, %d icons",
		verb, len(r.Created), len(r.Skipped), len(r.Rejected), len(r.TagsCreated), len(r.Icons))
}

// SafeSVG reports whether an SVG image has no scripts, event handlers or
// embedded documents, so it can be shown as an icon.
func SafeSVG(b []byte) bool { return !unsafeSVG.Match(b) }

// writeIconData stores icons given inline (as base64), with the same checks
// as icon files.
func (r *Report) writeIconData(dir string, data map[string]string, ok map[string]bool, dryRun bool) {
	for _, name := range sortedKeys(data) {
		b, err := base64.StdEncoding.DecodeString(data[name])
		switch {
		case err != nil:
			err = errors.New("not base64")
		case !domain.ValidIconRef("file:"+name, false):
			err = errors.New("the name must be lowercase letters, digits, '-' or '_' with .png, .jpg, .ico or .svg")
		case len(b) == 0 || len(b) > MaxIconBytes:
			err = fmt.Errorf("larger than %d KB", MaxIconBytes>>10)
		case strings.HasSuffix(name, ".svg") && !SafeSVG(b):
			err = errors.New("the SVG contains scripts or event handlers")
		}
		if err == nil && !dryRun {
			if err = os.MkdirAll(dir, 0o700); err == nil {
				err = os.WriteFile(filepath.Join(dir, name), b, 0o600)
			}
		}
		if err != nil {
			r.IconProblems = append(r.IconProblems, Entry{name, err.Error()})
			delete(ok, name)
			continue
		}
		ok[name] = true
		r.Icons = append(r.Icons, name)
	}
}

// kinds creates the user kinds the catalog does not have yet (matched by name).
func (r *Report) kinds(cat *catalog.File, in []Kind) {
	for _, k := range in {
		name := strings.TrimSpace(k.Name)
		if name == "" {
			continue
		}
		if _, err := cat.FindKind(name); err == nil {
			continue
		}
		if !domain.IsBuiltinKind(string(k.Base)) {
			r.Rejected = append(r.Rejected, Entry{"kind " + name, fmt.Sprintf("base %q is not one of %v", k.Base, domain.Kinds)})
			continue
		}
		cat.Kinds = append(cat.Kinds, domain.NewKind(name, k.Base, k.Color))
	}
}
