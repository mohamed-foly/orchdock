package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mohamed-foly/orchdock/internal/catalog"
	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

func init() {
	register("ls", "[--kind k] [--tag t] [--group tag|kind] [--pinned] [--running] [--failed] [--json]", "list items", cmdLs)
	register("add", "<kind> <title> [flags]", "add an agent, service, task, shell or link", cmdAdd)
	register("edit", "<ref> [flags]", "change an item", cmdEdit)
	register("rm", "<ref>", "remove an item (it must not be running)", cmdRm)
}

// itemRow is one line of "ls".
type itemRow struct {
	ID          string   `json:"id"`
	Alias       string   `json:"alias"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`     // the behaviour (stable for scripts)
	KindName    string   `json:"kindName"` // what the user calls it
	Pinned      bool     `json:"pinned,omitempty"`
	State       string   `json:"state"` // running | interrupted | idle
	LastOutcome string   `json:"lastOutcome,omitempty"`
	LastRunAt   string   `json:"lastRunAt,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func cmdLs(e *Env, args []string) error {
	fs := newFlags("ls", e.Err)
	kind := fs.String("kind", "", "only this kind")
	tag := fs.String("tag", "", "only items with this tag")
	running := fs.Bool("running", false, "only running items")
	failed := fs.Bool("failed", false, "only items whose last run failed")
	pinned := fs.Bool("pinned", false, "only pinned items")
	group := fs.String("group", "", "group the list by tag (an item under each of its tags) or kind")
	asJSON := fs.Bool("json", false, "machine-readable output")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 0, "ls [flags]"); err != nil {
		return err
	}
	if *group != "" && *group != "tag" && *group != "kind" {
		return fmt.Errorf("%w: --group is tag or kind", domain.ErrInvalid)
	}
	cat, err := e.Store.Load()
	if err != nil {
		return err
	}
	var wantBase domain.Kind
	wantKindID := ""
	if *kind != "" {
		if wantBase, wantKindID, err = cat.ResolveKind(*kind); err != nil {
			return err
		}
	}
	kindNames := map[string]string{}
	for _, k := range cat.AllKinds() {
		kindNames[k.ID] = k.Name
	}
	runs, _ := e.Runner.AllRuns() // newest first within an item
	state := map[string]runner.State{}
	for _, r := range runs {
		// The newest run decides; a running run always wins.
		if cur, ok := state[r.ItemID]; !ok || (r.State == runner.StateRunning && cur != runner.StateRunning) ||
			(r.State == runner.StateHeld && cur != runner.StateRunning && cur != runner.StateHeld) {
			state[r.ItemID] = r.State
		}
	}
	var rows []itemRow
	for _, it := range cat.Items {
		if it.Hidden || (*kind != "" && (it.Kind != wantBase || (wantKindID != "" && it.KindID != wantKindID))) || (*tag != "" && !slices.Contains(it.Tags, *tag)) {
			continue
		}
		if *pinned && !it.Pinned {
			continue
		}
		row := itemRow{ID: it.ID, Alias: it.Alias, Title: it.Title, Kind: string(it.Kind), KindName: kindNames[string(it.Kind)], State: "idle", Tags: it.Tags}
		if it.KindID != "" {
			row.KindName = kindNames[it.KindID]
		}
		row.Pinned = it.Pinned
		switch state[it.ID] {
		case runner.StateRunning:
			row.State = "running"
		case runner.StateHeld:
			row.State = "held"
		case runner.StateInterrupted:
			row.State = "interrupted"
		}
		if hist, _ := e.Runner.History(it.ID); len(hist) > 0 {
			last := hist[len(hist)-1]
			row.LastOutcome = last.Outcome
			if last.ExitCode != nil && last.Outcome == domain.OutcomeFailed {
				row.LastOutcome = fmt.Sprintf("failed (%d)", *last.ExitCode)
			}
		}
		if !it.LastRunAt.IsZero() {
			row.LastRunAt = it.LastRunAt.UTC().Format(time.RFC3339)
		}
		if *running && row.State != "running" {
			continue
		}
		if *failed && !strings.HasPrefix(row.LastOutcome, "failed") {
			continue
		}
		rows = append(rows, row)
	}
	if *asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		if rows == nil {
			rows = []itemRow{}
		}
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(e.Out, "no items")
		return nil
	}
	if *group == "" {
		return printRows(e, rows)
	}
	keys := func(r itemRow) []string { // the groups a row belongs to
		if *group == "kind" {
			return []string{r.KindName}
		}
		if len(r.Tags) == 0 {
			return []string{untaggedGroup}
		}
		return r.Tags
	}
	order := groupOrder(rows, keys)
	for i, g := range order {
		if i > 0 {
			fmt.Fprintln(e.Out)
		}
		var in []itemRow
		for _, r := range rows {
			if slices.Contains(keys(r), g) {
				in = append(in, r)
			}
		}
		fmt.Fprintf(e.Out, "%s (%d)\n", g, len(in))
		if err := printRows(e, in); err != nil {
			return err
		}
	}
	return nil
}

// untaggedGroup heads the items without tags when grouping by tag.
const untaggedGroup = "Untagged"

// groupOrder lists the group names in first-seen order; Untagged goes last.
func groupOrder(rows []itemRow, keys func(itemRow) []string) []string {
	var order []string
	for _, r := range rows {
		for _, g := range keys(r) {
			if !slices.Contains(order, g) {
				order = append(order, g)
			}
		}
	}
	if i := slices.Index(order, untaggedGroup); i >= 0 {
		order = append(append(order[:i:i], order[i+1:]...), untaggedGroup)
	}
	return order
}

func printRows(e *Env, rows []itemRow) error {
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tKIND\tSTATE\tLAST\tTITLE\tTAGS")
	for _, r := range rows {
		last, alias := r.LastOutcome, r.Alias
		if last == "" {
			last = "-"
		}
		if r.Pinned {
			alias += " *"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", alias, r.KindName, r.State, last, r.Title, strings.Join(r.Tags, ","))
	}
	return tw.Flush()
}

func cmdAdd(e *Env, args []string) error {
	fs := newFlags("add", e.Err)
	alias := fs.String("alias", "", "short name (default: made from the title)")
	line := fs.String("cmd", "", "command line, run through the shell")
	argv := fs.String("argv", "", "executable and arguments, comma separated, run without a shell")
	shell := fs.String("shell", "", "auto, pwsh, powershell, cmd, bash, zsh, sh")
	cwd := fs.String("cwd", "", "working folder (placeholders allowed)")
	var tags, envs stringList
	fs.Var(&tags, "tag", "tag (repeatable, or comma separated)")
	fs.Var(&envs, "env", "KEY=VALUE (repeatable)")
	keepOpen := fs.Bool("keep-open", false, "keep the terminal open after the command ends")
	stop := fs.String("stop", "", "stop preset: default, agent or custom")
	stopText := fs.String("stop-text", "", "text typed first by the custom stop preset")
	target := fs.String("target", "", "for a link: the folder, file or URL")
	schedule := addScheduleFlags(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 2, -1, "add <kind> <title> [flags]"); err != nil {
		return err
	}
	cat, err := e.Store.Load()
	if err != nil {
		return err
	}
	kind, kindID, err := cat.ResolveKind(pos[0]) // a built-in kind, or one of the user's
	if err != nil {
		return err
	}
	if *line != "" && *argv != "" {
		return fmt.Errorf("%w: use either --cmd or --argv, not both", domain.ErrInvalid)
	}
	env, err := parseEnv(envs)
	if err != nil {
		return err
	}
	title := strings.Join(pos[1:], " ")
	var created domain.Item
	err = e.Store.Update(func(f *catalog.File) error {
		it := f.NewItem(kind, title, *alias, time.Now())
		it.KindID = kindID
		if kind != domain.KindLink {
			it.Command.Line = *line
			if *argv != "" {
				it.Command.Argv, it.Command.Shell = splitList(*argv), domain.ShellNone
			} else if *shell != "" {
				it.Command.Shell = *shell
			}
			it.Command.Cwd = *cwd
			if len(env) > 0 {
				it.Command.Env = env
			}
		}
		if kind == domain.KindLink {
			it.Link = &domain.Link{Target: *target}
		}
		for _, t := range tags {
			it.Tags = append(it.Tags, splitList(t)...)
		}
		it.Interactive.KeepOpenOnExit = *keepOpen
		if *stop != "" {
			it.Interactive.StopSequence = *stop
		}
		it.Interactive.StopText = *stopText
		if err := schedule.apply(&it, isSet(fs), time.Now()); err != nil {
			return err
		}
		f.Items = append(f.Items, it)
		created = it
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(e.Out, created.Alias)
	if cat, err := e.Store.Load(); err == nil { // tags whose folder holds the item are only suggested
		var missing []string
		for _, t := range cat.SuggestTags(created.Command.Cwd) {
			if !slices.Contains(created.Tags, t) {
				missing = append(missing, t)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(e.Err, "suggested tag: %s (its folder is inside); add with: orchdock edit %s --add-tag %s\n", strings.Join(missing, ", "), created.Alias, missing[0])
		}
	}
	return nil
}

func cmdEdit(e *Env, args []string) error {
	fs := newFlags("edit", e.Err)
	title := fs.String("title", "", "new title")
	alias := fs.String("alias", "", "new alias")
	line := fs.String("cmd", "", "new command line")
	argv := fs.String("argv", "", "new executable and arguments, comma separated")
	shell := fs.String("shell", "", "new shell")
	cwd := fs.String("cwd", "", "new working folder")
	notes := fs.String("notes", "", "new notes")
	var addTags, rmTags, envs, unsetEnv stringList
	fs.Var(&addTags, "add-tag", "add a tag (repeatable)")
	fs.Var(&rmTags, "remove-tag", "remove a tag (repeatable)")
	fs.Var(&envs, "env", "set KEY=VALUE (repeatable)")
	fs.Var(&unsetEnv, "unset-env", "remove a variable (repeatable)")
	keepOpen := fs.Bool("keep-open", false, "keep the terminal open after the command ends")
	stop := fs.String("stop", "", "stop preset: default, agent or custom")
	stopText := fs.String("stop-text", "", "text typed first by the custom stop preset")
	target := fs.String("target", "", "for a link: the folder, file or URL")
	schedule := addScheduleFlags(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "edit <ref> [flags]"); err != nil {
		return err
	}
	set := isSet(fs)
	if set["cmd"] && set["argv"] {
		return fmt.Errorf("%w: use either --cmd or --argv, not both", domain.ErrInvalid)
	}
	env, err := parseEnv(envs)
	if err != nil {
		return err
	}
	return e.Store.Update(func(f *catalog.File) error {
		found, err := runner.FindItem(f.Items, pos[0])
		if err != nil {
			return err
		}
		i := slices.IndexFunc(f.Items, func(it domain.Item) bool { return it.ID == found.ID })
		it := &f.Items[i]
		if set["title"] {
			it.Title = *title
		}
		if set["alias"] && *alias != it.Alias {
			if f.AliasTaken(*alias) {
				return fmt.Errorf("%w: alias %q is already used", domain.ErrConflict, *alias)
			}
			it.Alias = *alias
		}
		if set["cmd"] {
			it.Command.Line, it.Command.Argv = *line, nil
			if it.Command.Shell == domain.ShellNone {
				it.Command.Shell = "auto"
			}
		}
		if set["argv"] {
			it.Command.Argv, it.Command.Line, it.Command.Shell = splitList(*argv), "", domain.ShellNone
		}
		if set["shell"] {
			it.Command.Shell = *shell
		}
		if set["cwd"] {
			it.Command.Cwd = *cwd
		}
		if set["notes"] {
			it.Notes = *notes
		}
		for _, t := range addTags {
			for _, one := range splitList(t) {
				if !slices.Contains(it.Tags, one) {
					it.Tags = append(it.Tags, one)
				}
			}
		}
		for _, t := range rmTags {
			it.Tags = slices.DeleteFunc(it.Tags, func(x string) bool { return x == t })
		}
		if len(env) > 0 || len(unsetEnv) > 0 {
			if it.Command.Env == nil {
				it.Command.Env = map[string]string{}
			}
			for k, v := range env {
				it.Command.Env[k] = v
			}
			for _, k := range unsetEnv {
				delete(it.Command.Env, k)
			}
		}
		if set["keep-open"] {
			it.Interactive.KeepOpenOnExit = *keepOpen
		}
		if set["stop"] {
			it.Interactive.StopSequence = *stop
		}
		if set["stop-text"] {
			it.Interactive.StopText = *stopText
		}
		if set["target"] && it.Link != nil {
			it.Link.Target = *target
		}
		if err := schedule.apply(it, set, time.Now()); err != nil {
			return err
		}
		it.UpdatedAt = time.Now()
		return nil
	})
}

func cmdRm(e *Env, args []string) error {
	fs := newFlags("rm", e.Err)
	permanent := fs.Bool("permanent", false, "delete for good instead of moving to the trash")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "rm <ref> [--permanent]"); err != nil {
		return err
	}
	cat, err := e.Store.Load()
	if err != nil {
		return err
	}
	it, err := runner.FindItem(cat.Items, pos[0])
	if err != nil {
		return err
	}
	if _, running := e.Runner.Attachable(it.ID); running {
		return fmt.Errorf("%w: %s is running; stop it first", domain.ErrConflict, it.Alias)
	}
	err = e.Store.Update(func(f *catalog.File) error {
		if *permanent {
			f.Items = slices.DeleteFunc(f.Items, func(x domain.Item) bool { return x.ID == it.ID })
			return nil
		}
		_, err := f.TrashItem(it.ID, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	if !*permanent {
		fmt.Fprintf(e.Out, "moved %s to the trash (orchdock trash restore %s brings it back within %d days)\n", it.Alias, it.Alias, domain.TrashDays)
		return nil
	}
	removeRunData(e.Home, it.ID)
	fmt.Fprintf(e.Out, "removed %s\n", it.Alias)
	return nil
}

// removeRunData deletes an item's runs and history.
func removeRunData(home, itemID string) {
	_ = os.RemoveAll(filepath.Join(home, "runs", itemID))
	_ = os.Remove(filepath.Join(home, "history", itemID+".jsonl"))
}

// scheduleFlags are the task scheduling options of add and edit.
type scheduleFlags struct {
	spec, missed, overlap *string
	timeout               *int
}

func addScheduleFlags(fs *flag.FlagSet) scheduleFlags {
	return scheduleFlags{
		spec:    fs.String("schedule", "", `when a task runs by itself: "every 2h", "daily 09:00", "weekly mon,thu 09:00", "monthly 1 08:00", "cron '*/5 * * * *'"; off pauses it, an empty value removes it`),
		missed:  fs.String("missed", "", "when a run was missed (computer off or asleep): skip, or catchup to run once when back"),
		overlap: fs.String("overlap", "", "when the previous run still goes: skip, queue or restart"),
		timeout: fs.Int("timeout", 0, "stop a scheduled run after this many minutes (0: no limit)"),
	}
}

// apply sets the flags that were given on a task item.
func (sf scheduleFlags) apply(it *domain.Item, set map[string]bool, now time.Time) error {
	if !set["schedule"] && !set["missed"] && !set["overlap"] && !set["timeout"] {
		return nil
	}
	if it.Task == nil {
		return fmt.Errorf("%w: only tasks can have a schedule", domain.ErrInvalid)
	}
	if set["schedule"] {
		switch strings.TrimSpace(*sf.spec) {
		case "":
			it.Task.Schedule = nil
		case "off":
			if it.Task.Schedule != nil {
				it.Task.Schedule.Enabled = false
			}
		case "on":
			if it.Task.Schedule == nil {
				return fmt.Errorf("%w: the task has no schedule to turn on", domain.ErrInvalid)
			}
			it.Task.Schedule.Enabled = true
		default:
			s, err := domain.ParseSpec(*sf.spec, now)
			if err != nil {
				return err
			}
			it.Task.Schedule = &s
		}
	}
	if set["missed"] {
		it.Task.Missed = *sf.missed
	}
	if set["overlap"] {
		it.Task.Overlap = *sf.overlap
	}
	if set["timeout"] {
		it.Task.TimeoutMinutes = *sf.timeout
	}
	return nil
}
