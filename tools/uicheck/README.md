# UI check

`check.mjs` loads the management window in a real Chrome (through the
DevTools protocol), clicks its controls and takes screenshots.

1. Start a service on a throw-away home that holds the sample items:
   `web-board` (a running service tagged `backlog,web`), `pull-all` (a task
   `git status`, tagged `daily,git`, run once), `fail3` (a task `exit 3`, run
   once), `scratch` (a shell) and `docs` (a link).
2. Start Chrome with `--remote-debugging-port=<port>` and its own
   `--user-data-dir`.
3. `node tools/uicheck/check.mjs http://127.0.0.1:<service-port> <port> <screenshot-dir>`
