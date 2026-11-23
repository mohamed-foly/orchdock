package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

func init() {
	register("tag", "ls | set <tag> [--folder dir] [--color c] | rename <tag> <new> | rm <tag> [--from-items]",
		"tag details: a folder (items inside it are offered the tag), a colour; tags organise items", cmdTag)
	register("pin", "<ref>", "pin an item to the top of the Home screen", func(e *Env, args []string) error { return setPinned(e, args, true) })
	register("unpin", "<ref>", "unpin an item", func(e *Env, args []string) error { return setPinned(e, args, false) })
}

func cmdTag(e *Env, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return tagLs(e)
	case "set":
		return tagSet(e, rest)
	case "rename":
		if err := need(rest, 2, 2, "tag rename <tag> <new>"); err != nil {
			return err
		}
		return e.Store.Update(func(f *catalog.File) error { return f.RenameTag(rest[0], strings.ToLower(rest[1])) })
	case "rm":
		fs := newFlags("tag rm", e.Err)
		fromItems := fs.Bool("from-items", false, "also take the tag off every item")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if err := need(pos, 1, 1, "tag rm <tag> [--from-items]"); err != nil {
			return err
		}
		return e.Store.Update(func(f *catalog.File) error { f.RemoveTagDef(pos[0], *fromItems); return nil })
	}
	return fmt.Errorf("%w: unknown tag command %q (ls, set, rename, rm)", domain.ErrInvalid, sub)
}

// tagLs lists every tag in use or defined, with its details.
func tagLs(e *Env) error {
	cat, err := e.Store.Load()
	if err != nil {
		return err
	}
	count := map[string]int{}
	for _, it := range cat.Items {
		for _, t := range it.Tags {
			count[t]++
		}
	}
	for _, d := range cat.Tags {
		if _, ok := count[d.Name]; !ok {
			count[d.Name] = 0
		}
	}
	if len(count) == 0 {
		fmt.Fprintln(e.Out, "no tags")
		return nil
	}
	names := make([]string, 0, len(count))
	for n := range count {
		names = append(names, n)
	}
	sort.Strings(names)
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TAG\tITEMS\tCOLOUR\tFOLDER")
	for _, n := range names {
		d, _ := cat.TagDefOf(n)
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", n, count[n], dash(d.Color), dash(d.Path))
	}
	return tw.Flush()
}

func tagSet(e *Env, args []string) error {
	fs := newFlags("tag set", e.Err)
	folder := fs.String("folder", "", "folder: items working inside it are offered the tag (empty: none)")
	color := fs.String("color", "", "colour like #4f8cff (empty: none)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "tag set <tag> [--folder dir] [--color c]"); err != nil {
		return err
	}
	name := strings.ToLower(pos[0])
	if !domain.ValidTag(name) {
		return fmt.Errorf("%w: %q must be 1-32 lowercase letters, digits, '.', '_' or '-'", domain.ErrInvalid, pos[0])
	}
	set := isSet(fs)
	return e.Store.Update(func(f *catalog.File) error {
		d, _ := f.TagDefOf(name)
		d.Name = name
		if set["folder"] {
			d.Path = *folder
		}
		if set["color"] {
			d.Color = *color
		}
		f.SetTagDef(d)
		return nil
	})
}

func setPinned(e *Env, args []string, on bool) error {
	if err := need(args, 1, 1, "pin <ref>"); err != nil {
		return err
	}
	return e.Store.Update(func(f *catalog.File) error {
		found, err := runner.FindItem(f.Items, args[0])
		if err != nil {
			return err
		}
		i := slices.IndexFunc(f.Items, func(it domain.Item) bool { return it.ID == found.ID })
		f.Items[i].Pinned = on
		f.Items[i].UpdatedAt = time.Now()
		return nil
	})
}
