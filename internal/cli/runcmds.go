package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/plain"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

func init() {
	register("run", "<ref> [--variant id] [--attach] [--wait [--raw]]", "run an item", cmdRun)
	register("stop", "<ref>|--all [--force]", "stop a running item", cmdStop)
	register("restart", "<ref> [--variant id]", "stop an item if it runs, then run it", cmdRestart)
	register("send", "<ref> <text> [--enter]", "type text into a running item's terminal", cmdSend)
	register("logs", "<ref> [--run id] [--follow] [--tail n] [--raw]", "show a run's output", cmdLogs)
	register("history", "<ref> [--json]", "show an item's past runs", cmdHistory)
	register("cmd", "<ref> [--variant id] [--bare]", "print the command as text to paste into a terminal", cmdCmd)
}

func cmdRun(e *Env, args []string) error {
	fs := newFlags("run", e.Err)
	variant := fs.String("variant", "", "command variant to use")
	attachFlag := fs.Bool("attach", false, "attach this terminal to the run")
	wait := fs.Bool("wait", false, "wait for the run to end, print its output and exit with its exit code")
	raw := fs.Bool("raw", false, "with --wait: print the terminal stream as it is instead of plain text")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "run <ref> [--variant id] [--attach] [--wait]"); err != nil {
		return err
	}
	run, err := e.Runner.Start(pos[0], runner.StartOptions{Variant: *variant})
	if err != nil {
		return err
	}
	alias := aliasOf(e, run.ItemID)
	switch {
	case *attachFlag:
		return attachRun(e, run, alias)
	case *wait:
		fmt.Fprintf(e.Err, "started %s (run %s)\n", alias, run.ID)
		return waitAndReport(e, run, plain.OutputWriter(e.Out, *raw))
	}
	fmt.Fprintf(e.Out, "started %s (run %s)\n", alias, run.ID)
	return nil
}

// waitAndReport streams the run's output until it ends and turns its result
// into this command's exit code.
func waitAndReport(e *Env, run runner.Run, out io.Writer) error {
	logPath := filepath.Join(run.Dir, host.LogFile)
	var offset int64
	for {
		cur, _ := runner.Inspect(run.Dir)
		offset = copyNew(logPath, offset, out)
		if cur.State != runner.StateRunning {
			e.Runner.Reconcile(run.ItemID)
			if cur.State == runner.StateInterrupted || cur.Result == nil {
				return &ExitError{Code: ExitUsage, Msg: "the run was interrupted"}
			}
			if code := cur.Result.ExitCode; code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// copyNew writes the bytes of path after offset to out and returns the new
// offset. If the file shrank (the host trimmed it) it starts again at the top.
func copyNew(path string, offset int64, out io.Writer) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return offset
	}
	if st.Size() < offset {
		offset = 0
	}
	if st.Size() == offset {
		return offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	n, _ := io.Copy(out, f)
	return offset + n
}

func aliasOf(e *Env, itemID string) string {
	cat, err := e.Store.Load()
	if err == nil {
		for _, it := range cat.Items {
			if it.ID == itemID {
				return it.Alias
			}
		}
	}
	return itemID
}

func cmdStop(e *Env, args []string) error {
	fs := newFlags("stop", e.Err)
	force := fs.Bool("force", false, "end the whole process tree at once")
	all := fs.Bool("all", false, "stop every running item")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *all {
		if err := need(pos, 0, 0, "stop --all [--force]"); err != nil {
			return err
		}
		runs, _ := e.Runner.AllRuns()
		var failures []string
		for _, r := range runs {
			if r.State != runner.StateRunning && r.State != runner.StateHeld {
				continue
			}
			if _, err := e.Runner.Stop(r.ItemID, runner.StopOptions{Force: *force}); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", aliasOf(e, r.ItemID), err))
				continue
			}
			fmt.Fprintf(e.Out, "stopped %s\n", aliasOf(e, r.ItemID))
		}
		if len(failures) > 0 {
			return fmt.Errorf("could not stop everything:\n  %s", strings.Join(failures, "\n  "))
		}
		return nil
	}
	if err := need(pos, 1, 1, "stop <ref>|--all [--force]"); err != nil {
		return err
	}
	if _, err := e.Runner.Stop(pos[0], runner.StopOptions{Force: *force}); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "stopped %s\n", pos[0])
	return nil
}

func cmdRestart(e *Env, args []string) error {
	fs := newFlags("restart", e.Err)
	variant := fs.String("variant", "", "command variant to use")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "restart <ref> [--variant id]"); err != nil {
		return err
	}
	run, err := e.Runner.Restart(pos[0], runner.StartOptions{Variant: *variant})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "restarted %s (run %s)\n", aliasOf(e, run.ItemID), run.ID)
	return nil
}

func cmdSend(e *Env, args []string) error {
	fs := newFlags("send", e.Err)
	enter := fs.Bool("enter", false, "press Enter after the text")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 2, 2, "send <ref> <text> [--enter]"); err != nil {
		return err
	}
	run, err := activeRun(e, pos[0])
	if err != nil {
		return err
	}
	text := pos[1]
	if *enter {
		text += "\r"
	}
	return host.SendInput(run.Dir, []byte(text))
}

// activeRun finds the running run of an item, or a run by its id (r_...).
func activeRun(e *Env, ref string) (runner.Run, error) {
	if strings.HasPrefix(ref, domain.PrefixRun) {
		runs, _ := e.Runner.AllRuns()
		for _, r := range runs {
			if r.ID == ref || (len(ref) >= 6 && strings.HasPrefix(r.ID, ref)) {
				if r.State != runner.StateRunning && r.State != runner.StateHeld {
					return r, fmt.Errorf("%w: run %s is not running", domain.ErrNotFound, r.ID)
				}
				return r, nil
			}
		}
		return runner.Run{}, fmt.Errorf("%w: no run %q", domain.ErrNotFound, ref)
	}
	cat, err := e.Store.Load()
	if err != nil {
		return runner.Run{}, err
	}
	it, err := runner.FindItem(cat.Items, ref)
	if err != nil {
		return runner.Run{}, err
	}
	run, ok := e.Runner.Attachable(it.ID)
	if !ok {
		return runner.Run{}, fmt.Errorf("%w: %s is not running", domain.ErrNotFound, it.Alias)
	}
	return run, nil
}

func cmdLogs(e *Env, args []string) error {
	fs := newFlags("logs", e.Err)
	runID := fs.String("run", "", "a specific run (default: the running or newest run)")
	follow := fs.Bool("follow", false, "keep printing new output until the run ends")
	tail := fs.Int("tail", 200, "show only the last n lines (0 shows everything)")
	raw := fs.Bool("raw", false, "print the terminal stream as it is instead of plain text")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "logs <ref> [--run id] [--follow] [--tail n]"); err != nil {
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
	runs, err := e.Runner.Runs(it.ID)
	if err != nil {
		return err
	}
	var run runner.Run
	for _, r := range runs { // newest first
		if *runID != "" && r.ID != *runID && !strings.HasPrefix(r.ID, *runID) {
			continue
		}
		if run.ID == "" || r.State == runner.StateRunning {
			run = r
		}
		if *runID != "" || r.State == runner.StateRunning {
			break
		}
	}
	if run.ID == "" {
		return fmt.Errorf("%w: %s has no runs", domain.ErrNotFound, it.Alias)
	}
	logPath := filepath.Join(run.Dir, host.LogFile)
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out := plain.OutputWriter(e.Out, *raw)
	if *tail > 0 && (*raw || plain.IsTerminal(e.Out)) {
		data = lastLines(data, *tail)
	} else if *tail > 0 {
		data = lastLines(plain.Bytes(data), *tail)
	}
	if _, err := e.Out.Write(data); err != nil {
		return err
	}
	if !*follow {
		return nil
	}
	st, _ := os.Stat(logPath)
	var offset int64
	if st != nil {
		offset = st.Size()
	}
	for {
		cur, _ := runner.Inspect(run.Dir)
		offset = copyNew(logPath, offset, out)
		if cur.State != runner.StateRunning {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// lastLines returns the last n lines of data.
func lastLines(data []byte, n int) []byte {
	trimmed := bytes.TrimRight(data, "\r\n")
	idx := len(trimmed)
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(trimmed[:idx], '\n')
		if j < 0 {
			return data
		}
		idx = j
	}
	return data[idx+1:]
}

func cmdHistory(e *Env, args []string) error {
	fs := newFlags("history", e.Err)
	asJSON := fs.Bool("json", false, "machine-readable output")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "history <ref> [--json]"); err != nil {
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
	e.Runner.Reconcile(it.ID)
	lines, err := e.Runner.History(it.ID)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		if lines == nil {
			lines = []domain.HistoryLine{}
		}
		return enc.Encode(lines)
	}
	if len(lines) == 0 {
		fmt.Fprintf(e.Out, "%s has not run yet\n", it.Alias)
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STARTED\tOUTCOME\tEXIT\tTOOK\tTRIGGER\tVARIANT\tRUN")
	for i := len(lines) - 1; i >= 0; i-- { // newest first
		l := lines[i]
		exit := "-"
		if l.ExitCode != nil {
			exit = fmt.Sprint(*l.ExitCode)
		}
		took := "-"
		if l.DurationMs > 0 {
			took = (time.Duration(l.DurationMs) * time.Millisecond).Round(10 * time.Millisecond).String()
		}
		when := "-"
		if !l.StartedAt.IsZero() {
			when = l.StartedAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", when, l.Outcome, exit, took, l.Trigger, orDash(l.Variant), orDash(l.RunID))
	}
	return tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdCmd(e *Env, args []string) error {
	fs := newFlags("cmd", e.Err)
	variant := fs.String("variant", "", "command variant")
	bare := fs.Bool("bare", false, "only the command, without cd and environment")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "cmd <ref> [--variant id] [--bare]"); err != nil {
		return err
	}
	text, err := e.Runner.CommandText(pos[0], *variant, *bare)
	if err != nil {
		return err
	}
	fmt.Fprintln(e.Out, text)
	return nil
}
