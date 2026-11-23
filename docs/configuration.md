# Configuration

## The data folder

OrchDock keeps everything in one folder, its *home*: the catalog of items
(`catalog.json`), settings (`config.json`), run folders and logs (`runs/`),
history (`history/`), icons (`icons/`) and state (`state/`).

The home is chosen in this order:

1. `--home <folder>` on the command line;
2. the `ORCHDOCK_HOME` environment variable (also read from `.env`, below);
3. a `data` folder next to the programs, when that folder can be written (a
   portable install: copy the folder, and OrchDock comes with its data);
4. your per-user data folder: `%LOCALAPPDATA%\orchdock` on Windows,
   `~/Library/Application Support/orchdock` on macOS, `~/.local/share/orchdock`
   on Linux.

`orchdock install` writes `ORCHDOCK_HOME` into the `.env` of the installed
programs: the data folder you were already using, or else your per-user data
folder. So installing a portable copy keeps its data where it is.

`orchdock home` prints the folder in use.

The home holds your commands, folders and environment variables, and possibly
secrets in them. Keep it private, and do not put it in a shared or synced
folder you would not trust with them.

## .env

Machine-specific settings go in a `.env` file next to the `orchdock` and
`orchdockw` programs, so they never end up in a repository or a shell profile.
Copy [`.env.example`](../.env.example) and change what you need. Only
`ORCHDOCK_*` keys are read, and a variable set in the real environment always
wins.

| Variable | Meaning |
|---|---|
| `ORCHDOCK_HOME` | the data folder (see above) |

## Settings (`config.json`)

`config.json` in the home holds the settings. It is created with defaults; the
service reads it when it starts. The main ones:

| Key | Default | Meaning |
|---|---|---|
| `uiPort` | 7400 | port of the window and API (a free one is used if it is taken) |
| `servicePortRange` | `[7401, 7499]` | where automatic service ports come from |
| `maxConcurrentRuns` | 64 | runs that may be active at once |
| `defaultShell` | `auto` | shell for command lines (`auto` picks pwsh, then powershell on Windows; sh elsewhere) |
| `terminal.detachKey` | `ctrl+]` | key that detaches `orchdock attach` |
| `terminal.logMaxBytes` | 10 MB | size kept per run log (the end is kept) |
| `history.runsPerItem`, `history.maxAgeDays` | 50, 30 | how many old runs are kept, and for how many days |

## Starting at sign-in

`orchdock autostart on` makes OrchDock start in the tray when you sign in, so
schedules run without opening it by hand; `off` removes it. On Windows this is
a per-user entry in the sign-in programs list (nothing system-wide). Other
systems are not supported yet.

## Security

- The service listens on `127.0.0.1` only and every API call needs a random
  token written to `server.json` in the home (readable only by you).
- Each run's host listens on `127.0.0.1` with its own token.
- Links open addresses in your browser and folders in your file manager; a link
  to a file only shows the file, it never runs it.
- Imported SVG icons with scripts or event handlers are refused.
- No telemetry, no network access beyond your own machine.
