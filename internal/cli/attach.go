package cli

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/mohamed-foly/orchdock/internal/attach"
	"github.com/mohamed-foly/orchdock/internal/host"
	"github.com/mohamed-foly/orchdock/internal/runner"
)

func init() {
	register("attach", "<ref|runId>", "show a running terminal here (Ctrl+] detaches, the run keeps going)", cmdAttach)
}

func cmdAttach(e *Env, args []string) error {
	fs := newFlags("attach", e.Err)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 1, 1, "attach <ref|runId>"); err != nil {
		return err
	}
	run, err := activeRun(e, pos[0])
	if err != nil {
		return err
	}
	return attachRun(e, run, aliasOf(e, run.ItemID))
}

// attachRun connects this terminal to a running run.
func attachRun(e *Env, run runner.Run, alias string) error {
	info, err := host.ReadInfo(run.Dir)
	if err != nil {
		return fmt.Errorf("%w: %v", host.ErrNoHost, err)
	}
	// looking at the terminal answers the item's call; best effort, the service may not run
	_ = callService(e, http.MethodPost, "/api/attention/"+url.PathEscape(run.ItemID)+"/clear", struct{}{})
	return attach.Interactive(info, alias, e.Home, Stdin, e.Out)
}
