package cli

import (
	"fmt"

	"github.com/mohamed-foly/orchdock/internal/autostart"
	"github.com/mohamed-foly/orchdock/internal/domain"
)

func init() {
	register("autostart", "[on|off]", "start OrchDock in the tray when you sign in (so schedules run)", cmdAutostart)
}

func cmdAutostart(e *Env, args []string) error {
	fs := newFlags("autostart", e.Err)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := need(pos, 0, 1, "autostart [on|off]"); err != nil {
		return err
	}
	if len(pos) == 1 {
		switch pos[0] {
		case "on", "off":
			if err := autostart.Set(pos[0] == "on", windowlessBinary()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: autostart on|off", domain.ErrInvalid)
		}
	}
	on, cmd, err := autostart.Enabled()
	if err != nil {
		return err
	}
	if on {
		fmt.Fprintf(e.Out, "on: %s\n", cmd)
	} else {
		fmt.Fprintln(e.Out, "off")
	}
	return nil
}
