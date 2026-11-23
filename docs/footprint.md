# Footprint

OrchDock is meant to sit in the tray all day, so its size and memory use are
kept small and checked. The release build fails when a program is larger than
25 MB (`go run ./tools/build`, flag `-budget`).

Measured on Windows 11 (x64) with an empty catalog, development version,
November 2026:

| What | Size / use |
| --- | --- |
| `orchdock.exe` (command line), release build | 14.8 MB |
| `orchdockw.exe` (tray and window), release build | 14.8 MB |
| Release archive (both programs, zipped) | about 12 MB |
| `orchdock version`, cold start | about 25 ms |
| Tray start until the service answers | about 250 ms |
| Tray only, window closed | about 75 MB working set |
| Each running item (its host process) | about 37 MB working set |
| Window open | about 85 MB for OrchDock, plus the system WebView2 (about 320 MB, shared with other WebView2 apps and released when the window closes) |

The window uses the WebView2 runtime that ships with Windows; nothing else is
installed. Closing the window keeps only the tray and the hosts of running items.

To measure again: build with `go run ./tools/build`, start `orchdockw ui --tray`
with an empty data folder (`ORCHDOCK_HOME`), and read the working set of the
`orchdockw.exe` processes in Task Manager.
