# FAQ

**Does a run stop when I close the window or quit OrchDock?**
No. Each run lives in its own host process that owns its terminal. Closing the
window, quitting OrchDock or restarting the service leaves runs going; the window
picks them up again. Only **Stop** (or the program ending) ends a run.

**What happens to runs when the computer restarts?**
They end with the computer. Their history shows them as interrupted.

**How do I get back into a running item?**
**Terminal** in the window, or `orchdock attach <alias>` in any terminal.
**Ctrl+]** detaches again without stopping the run.

**My shell aliases don't work in items.**
Aliases and functions from your shell profile do not exist for programs started
by OrchDock. Put the full command in the item, or use variants for the
different ways you run it.

**Does OrchDock watch programs I started myself?**
No. It only knows the runs it started.

**Why did my service not start on its port?**
Another program already listens there; the error and the **Ports** dialog name
it. Stop that program or give the service another port. OrchDock never moves a
service to another port by itself.

**An agent waits for me but nothing tells me.**
OrchDock reacts to the terminal bell. Turn on your agent's bell or notification
setting (many coding agents have one), or call `orchdock notify <alias>` from a
hook or script.

**My scheduled task did not run.**
Schedules run only while OrchDock runs: turn on `orchdock autostart on`. A run
missed while the computer was off is skipped unless the task says *run once when
back*.

**How do I uninstall?**
Use Windows Settings ▸ Apps, or run `orchdock uninstall`. It removes the
programs, the Start-menu entry, the PATH entry and the start at sign-in; your
data folder stays (delete it yourself if you want it gone). A portable copy is
removed by deleting its folder.

**Where is my data, and how do I move it?**
`orchdock home` prints the folder. Copy it (with OrchDock stopped) and point
`ORCHDOCK_HOME` at the copy, or keep a portable `data` folder next to the
programs.
