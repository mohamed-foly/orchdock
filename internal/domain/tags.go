package domain

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// TagDef describes a tag: an optional folder (items working inside it are
// offered the tag), a colour and an icon. Tags are the one way items are
// organised; an item can have several. A tag needs no
// definition to be used; a definition only adds these details.
type TagDef struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Color string `json:"color,omitempty"`
	Icon  Icon   `json:"icon,omitzero"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// TagSlug turns a name such as "Corner Shop" into a tag ("corner-shop").
func TagSlug(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-._")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-._")
	}
	if s == "" {
		s = "tag"
	}
	return s
}

// TagDefOf returns the definition of a tag, if there is one.
func (cat Catalog) TagDefOf(name string) (TagDef, bool) {
	for _, d := range cat.Tags {
		if d.Name == name {
			return d, true
		}
	}
	return TagDef{}, false
}

// SetTagDef adds or replaces a tag definition.
func (cat *Catalog) SetTagDef(d TagDef) {
	if i := slices.IndexFunc(cat.Tags, func(x TagDef) bool { return x.Name == d.Name }); i >= 0 {
		cat.Tags[i] = d
		return
	}
	cat.Tags = append(cat.Tags, d)
}

// HomeOf is the item's main tag: its first tag that has a folder. It gives
// the item's default working folder and {projectDir}. nil when none.
func (cat Catalog) HomeOf(it Item) *TagDef {
	for _, t := range it.Tags {
		if d, ok := cat.TagDefOf(t); ok && d.Path != "" {
			return &d
		}
	}
	return nil
}

// SuggestTags lists the tags whose folder holds dir, the deepest first.
// Separators and, on Windows, letter case do not matter.
func (cat Catalog) SuggestTags(dir string) []string {
	d := normPath(dir, runtime.GOOS == "windows")
	if d == "" {
		return nil
	}
	type hit struct {
		name string
		n    int
	}
	var hits []hit
	for _, t := range cat.Tags {
		root := normPath(t.Path, runtime.GOOS == "windows")
		if root != "" && (d == root || strings.HasPrefix(d, root+"/")) {
			hits = append(hits, hit{t.Name, len(root)})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return b.n - a.n })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// normPath makes a folder comparable: forward slashes, no trailing slash,
// lower case when fold is set. Paths with placeholders compare as "".
func normPath(p string, fold bool) string {
	if strings.Contains(p, "{") {
		return ""
	}
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if fold {
		p = strings.ToLower(p)
	}
	return p
}

// RenameTag renames a tag everywhere: its definition, items (also in the
// trash), task targets and searches that name it.
func (cat *Catalog) RenameTag(from, to string) error {
	if !ValidTag(to) {
		return fmt.Errorf("%w: %q must be 1-32 lowercase letters, digits, '.', '_' or '-'", ErrInvalid, to)
	}
	if from == to {
		return nil
	}
	if _, exists := cat.TagDefOf(to); exists {
		return fmt.Errorf("%w: the tag %q already has its own settings", ErrConflict, to)
	}
	for i := range cat.Tags {
		if cat.Tags[i].Name == from {
			cat.Tags[i].Name = to
		}
	}
	swap := func(it *Item) {
		for j, t := range it.Tags {
			if t == from {
				it.Tags[j] = to
			}
		}
		it.Tags = slices.Compact(sortedUnique(it.Tags))
		if it.Task != nil {
			for j, t := range it.Task.Targets {
				if t == from {
					it.Task.Targets[j] = to
				}
			}
		}
	}
	for i := range cat.Items {
		swap(&cat.Items[i])
	}
	for i := range cat.Trash {
		swap(&cat.Trash[i].Item)
	}
	re := regexp.MustCompile(`(?i)(^|\s)tag:` + regexp.QuoteMeta(from) + `(\s|$)`)
	for i := range cat.Views {
		cat.Views[i].Query = re.ReplaceAllString(cat.Views[i].Query, "${1}tag:"+to+"${2}")
	}
	return nil
}

// sortedUnique keeps the first occurrence of each tag, in order.
func sortedUnique(tags []string) []string {
	out := tags[:0]
	seen := map[string]bool{}
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// RemoveTagDef deletes a tag's definition; with fromItems the tag is also
// taken off every item and task target.
func (cat *Catalog) RemoveTagDef(name string, fromItems bool) {
	cat.Tags = slices.DeleteFunc(cat.Tags, func(d TagDef) bool { return d.Name == name })
	if !fromItems {
		return
	}
	for i := range cat.Items {
		it := &cat.Items[i]
		it.Tags = slices.DeleteFunc(it.Tags, func(t string) bool { return t == name })
		if it.Task != nil {
			it.Task.Targets = slices.DeleteFunc(it.Task.Targets, func(t string) bool { return t == name })
		}
	}
}

// MigrateProjects turns the older projects into tags: each
// project becomes a tag definition with its folder, colour and icon, its
// items get the tag (first, so it stays their main tag), task targets name
// the tag, and a section (a saved search) is created for it. Tag colours
// move into the definitions. It reports whether anything changed.
func (cat *Catalog) MigrateProjects() bool {
	changed := false
	name := map[string]string{} // project id -> tag
	for _, p := range cat.Projects {
		t := TagSlug(p.Name)
		name[p.ID] = t
		d, _ := cat.TagDefOf(t)
		d.Name = t
		if d.Path == "" {
			d.Path = p.Path
		}
		if d.Color == "" {
			d.Color = p.Color
		}
		if d.Icon.Primary == "" {
			d.Icon = p.Icon
		}
		cat.SetTagDef(d)
		q := "tag:" + t
		if !slices.ContainsFunc(cat.Views, func(v View) bool { return v.Query == q }) {
			cat.Views = append(cat.Views, View{ID: NewID(PrefixView), Name: p.Name, Query: q})
		}
		changed = true
	}
	move := func(it *Item) {
		if t, ok := name[it.ProjectID]; ok && !slices.Contains(it.Tags, t) {
			it.Tags = append([]string{t}, it.Tags...)
		}
		if it.ProjectID != "" {
			it.ProjectID = ""
			changed = true
		}
		if it.Task != nil {
			for j, id := range it.Task.Targets {
				if t, ok := name[id]; ok {
					it.Task.Targets[j] = t
				}
			}
		}
	}
	for i := range cat.Items {
		move(&cat.Items[i])
	}
	for i := range cat.Trash {
		move(&cat.Trash[i].Item)
	}
	for tag, col := range cat.TagColors {
		d, _ := cat.TagDefOf(tag)
		d.Name = tag
		if d.Color == "" {
			d.Color = col
		}
		cat.SetTagDef(d)
		changed = true
	}
	cat.Projects = nil
	cat.TagColors = nil
	return changed
}

func (cat Catalog) validateTags(c *collector) {
	seen := map[string]bool{}
	for i, d := range cat.Tags {
		f := "tags[" + itoa(i) + "]"
		if !ValidTag(d.Name) {
			c.addf(f+".name", "%q must be 1-32 lowercase letters, digits, '.', '_' or '-'", d.Name)
		}
		if seen[d.Name] {
			c.addf(f+".name", "%q is defined twice", d.Name)
		}
		seen[d.Name] = true
		if d.Path != "" && !filepath.IsAbs(d.Path) && !isAbsAnyOS(d.Path) {
			c.addf(f+".path", "%q must be an absolute folder path", d.Path)
		}
		if d.Color != "" && !colorRe.MatchString(d.Color) {
			c.addf(f+".color", "%q is not a colour like #4f8cff", d.Color)
		}
		c.icon(f+".icon", d.Icon, true)
	}
}
