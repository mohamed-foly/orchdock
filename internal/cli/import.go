package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/mohamed-foly/orchdock/internal/importer"
)

func init() {
	register("import", "<file.json> [--dry-run]", "add projects, items and icons from a JSON file", cmdImport)
}

func cmdImport(e *Env, args []string) error {
	fs := newFlags("import", e.Err)
	dry := fs.Bool("dry-run", false, "show what would happen and change nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "import <file.json> [--dry-run]"); err != nil {
		return err
	}
	data, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	f, err := importer.Parse(data)
	if err != nil {
		return err
	}
	rep, err := importer.Run(e.Store, f, *dry, time.Now())
	if err != nil {
		return err
	}
	printReport(e, rep)
	return nil
}

func printReport(e *Env, r importer.Report) {
	list := func(title string, names []string) {
		if len(names) > 0 {
			fmt.Fprintf(e.Out, "%s (%d):\n", title, len(names))
			for _, n := range names {
				fmt.Fprintf(e.Out, "  %s\n", n)
			}
		}
	}
	entries := func(title string, es []importer.Entry) {
		if len(es) > 0 {
			fmt.Fprintf(e.Out, "%s (%d):\n", title, len(es))
			for _, x := range es {
				fmt.Fprintf(e.Out, "  %s: %s\n", x.Name, x.Reason)
			}
		}
	}
	list("new tags", r.TagsCreated)
	list("tags already here", r.TagsReused)
	list("icons", r.Icons)
	list("items", r.Created)
	entries("skipped", r.Skipped)
	entries("rejected", r.Rejected)
	entries("icon problems", r.IconProblems)
	fmt.Fprintln(e.Out, r.Summary())
}
