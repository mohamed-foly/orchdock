package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/importer"
)

func init() {
	register("trash", "ls | restore <ref> | empty", "list, restore or empty deleted items (kept 30 days)", cmdTrash)
	register("export", "[--include-env] [-o file]", "write all projects, kinds, items and icons as JSON (for import elsewhere)", cmdExport)
}

func cmdTrash(e *Env, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch args[0] {
	case "ls":
		var gone []string
		err := e.Store.Update(func(f *catalog.File) error { gone = f.PurgeTrash(time.Now(), false); return nil })
		if err != nil {
			return err
		}
		for _, id := range gone {
			removeRunData(e.Home, id)
		}
		cat, err := e.Store.Load()
		if err != nil {
			return err
		}
		if len(cat.Trash) == 0 {
			fmt.Fprintln(e.Out, "the trash is empty")
			return nil
		}
		tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ALIAS\tKIND\tDELETED\tTITLE")
		for i := len(cat.Trash) - 1; i >= 0; i-- {
			t := cat.Trash[i]
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Item.Alias, t.Item.Kind, t.DeletedAt.Local().Format("2006-01-02 15:04"), t.Item.Title)
		}
		return tw.Flush()
	case "restore":
		if err := need(args[1:], 1, 1, "trash restore <ref>"); err != nil {
			return err
		}
		var back domain.Item
		err := e.Store.Update(func(f *catalog.File) error {
			it, err := f.RestoreItem(args[1], time.Now())
			back = it
			return err
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "restored %s\n", back.Alias)
		return nil
	case "empty":
		var gone []string
		err := e.Store.Update(func(f *catalog.File) error { gone = f.PurgeTrash(time.Now(), true); return nil })
		if err != nil {
			return err
		}
		for _, id := range gone {
			removeRunData(e.Home, id)
		}
		fmt.Fprintf(e.Out, "deleted %d items for good\n", len(gone))
		return nil
	}
	return fmt.Errorf("%w: trash ls | restore <ref> | empty", domain.ErrInvalid)
}

func cmdExport(e *Env, args []string) error {
	fs := newFlags("export", e.Err)
	withEnv := fs.Bool("include-env", false, "include environment variable values (they may be secrets)")
	out := fs.String("o", "", "write to this file instead of the output")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 0, "export [--include-env] [-o file]"); err != nil {
		return err
	}
	cat, err := e.Store.Load()
	if err != nil {
		return err
	}
	b, err := importer.Export(cat, e.Home, *withEnv, time.Now())
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = e.Out.Write(append(b, '\n'))
		return err
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "wrote %s (%d items)\n", *out, len(cat.Items))
	return nil
}
