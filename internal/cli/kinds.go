package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

func init() {
	register("kind", "ls | add <name> --base <kind> [--color c] | edit <ref> [--name n] [--color c] | rm <ref> [--move-to k] | move <ref> up|down | set <item> <kind>",
		"manage your own kinds of items", cmdKind)
}

func cmdKind(e *Env, args []string) error {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "ls":
		return kindLs(e)
	case "add":
		return kindAdd(e, rest)
	case "edit":
		return kindEdit(e, rest)
	case "rm":
		return kindRm(e, rest)
	case "set":
		return kindSet(e, rest)
	case "move":
		if err := need(rest, 2, 2, "kind move <ref> up|down"); err != nil {
			return err
		}
		by := map[string]int{"up": -1, "down": 1}[rest[1]]
		if by == 0 {
			return fmt.Errorf("%w: kind move <ref> up|down", domain.ErrInvalid)
		}
		return e.Store.Update(func(f *catalog.File) error {
			k, err := f.FindKind(rest[0])
			if err != nil {
				return err
			}
			return f.MoveKind(k.ID, by)
		})
	default:
		return fmt.Errorf("%w: unknown kind command %q (ls, add, edit, rm, set)", domain.ErrInvalid, sub)
	}
}

func kindLs(e *Env) error {
	cat, err := e.Store.Load()
	if err != nil {
		return err
	}
	count := map[string]int{}
	for _, it := range cat.Items {
		if it.KindID != "" {
			count[it.KindID]++
		} else {
			count[string(it.Kind)]++
		}
	}
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBEHAVES AS\tITEMS\tCOLOUR\tID")
	for _, k := range cat.AllKinds() {
		color := k.Color
		if color == "" {
			color = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", k.Name, k.Base, count[k.ID], color, k.ID)
	}
	return tw.Flush()
}

func kindAdd(e *Env, args []string) error {
	fs := newFlags("kind add", e.Err)
	base := fs.String("base", "", "the built-in kind it behaves like: agent, service, task, shell or link")
	color := fs.String("color", "", "colour like #db2777")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, -1, "kind add <name> --base <kind> [--color c]"); err != nil {
		return err
	}
	if !domain.IsBuiltinKind(*base) {
		return fmt.Errorf("%w: --base must be one of %v", domain.ErrInvalid, domain.Kinds)
	}
	name := strings.Join(pos, " ")
	err = e.Store.Update(func(f *catalog.File) error {
		if f.KindNameTaken(name, "") {
			return fmt.Errorf("%w: a kind named %q exists", domain.ErrConflict, name)
		}
		f.Kinds = append(f.Kinds, domain.NewKind(name, domain.Kind(*base), *color))
		return nil
	})
	if err == nil {
		fmt.Fprintln(e.Out, name)
	}
	return err
}

func kindEdit(e *Env, args []string) error {
	fs := newFlags("kind edit", e.Err)
	name := fs.String("name", "", "new name")
	color := fs.String("color", "", "new colour (empty: none)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "kind edit <ref> [--name n] [--color c]"); err != nil {
		return err
	}
	set := isSet(fs)
	return e.Store.Update(func(f *catalog.File) error {
		k, err := f.FindKind(pos[0])
		if err != nil {
			return err
		}
		if set["name"] {
			if f.KindNameTaken(*name, k.ID) {
				return fmt.Errorf("%w: a kind named %q exists", domain.ErrConflict, *name)
			}
			k.Name = *name
		}
		if set["color"] {
			k.Color = *color
		}
		if i := slices.IndexFunc(f.Kinds, func(x domain.KindDef) bool { return x.ID == k.ID }); i >= 0 {
			f.Kinds[i] = k
		} else {
			f.Kinds = append(f.Kinds, k) // first change to a built-in kind
		}
		return nil
	})
}

func kindRm(e *Env, args []string) error {
	fs := newFlags("kind rm", e.Err)
	moveTo := fs.String("move-to", "", "kind for its items (default: the kind it is based on)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "kind rm <ref> [--move-to k]"); err != nil {
		return err
	}
	return e.Store.Update(func(f *catalog.File) error {
		k, err := f.FindKind(pos[0])
		if err != nil {
			return err
		}
		to := ""
		if *moveTo != "" {
			t, err := f.FindKind(*moveTo)
			if err != nil {
				return err
			}
			to = t.ID
		}
		return f.RemoveKind(k.ID, to)
	})
}

// kindSet gives an item one of the user's kinds (or its plain built-in kind).
func kindSet(e *Env, args []string) error {
	fs := newFlags("kind set", e.Err)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 2, 2, "kind set <item> <kind>"); err != nil {
		return err
	}
	return e.Store.Update(func(f *catalog.File) error {
		i := slices.IndexFunc(f.Items, func(it domain.Item) bool { return it.ID == pos[0] || it.Alias == pos[0] })
		if i < 0 {
			return fmt.Errorf("%w: no item %q", domain.ErrNotFound, pos[0])
		}
		base, id, err := f.ResolveKind(pos[1])
		if err != nil {
			return err
		}
		if base != f.Items[i].Kind {
			return fmt.Errorf("%w: %s is a %s; it can only get a kind based on %s", domain.ErrInvalid, f.Items[i].Alias, f.Items[i].Kind, f.Items[i].Kind)
		}
		f.Items[i].KindID = id
		return nil
	})
}
