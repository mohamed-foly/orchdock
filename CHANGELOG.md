# Changelog

All notable changes are listed here. The project is not released yet.

## Unreleased

### Added

- Runs in detached host processes that own their terminal; attach and detach
  from any terminal (`orchdock attach`, Ctrl+]).
- Items of five built-in kinds (task, service, agent, shell, link), user kinds,
  tags (with an optional folder, colour and icon), icons and command variants.
- Management window with a Home screen (pinned items and sections, which are
  saved searches), All items grouped by tag or kind, search, details with run
  history and logs, an editor, and Tags, Sections, Kinds, Ports and Import
  dialogs.
- Tags with a folder are suggested for new items made inside it (never added by
  themselves) and give their items a default working folder and icon.
- Tray icon and native window on Windows; single instance; runs keep going
  when the window or the app closes.
- Services with stable ports, refusal of ports held by other programs (naming
  them), health checks and restart policies.
- Scheduled tasks with skip / run-once-when-back for missed runs, overlap
  handling and time limits; start at sign-in.
- Attention from the terminal bell, failures and `orchdock notify`, shown in the
  window, on the tray icon and as desktop notifications.
- Import of tags, items and icons from a JSON file.
- Command line covering everything the window does.
- `orchdock install` / `uninstall` on Windows: per-user program folder,
  Start-menu entry, PATH with the `odock` alias, optional start at sign-in and
  an entry in Apps; data is never removed. Release builds stay under a 25 MB
  size budget; see docs/footprint.md.
- `.env` next to the programs for machine-specific settings.
- Icons: built-in line icons, emoji, uploaded images, monograms, automatic
  tag icons from the tag's folder, agent logos and services' own web icons.
- Bulk actions, a trash with restore, export (without
  environment values unless asked), and a Ctrl+K command palette.
- Privacy check in the test suite: no personal paths, addresses or private
  words in the repository.

### Fixed

- Items with shell `cmd` now run their line exactly as typed (quotes, quoted
  program paths with spaces), and npm-style `.cmd` shims in folders with spaces
  (such as Program Files) start correctly.

### Changed

- Projects are replaced by tags and sections. Catalogs and import files with
  projects are converted: each project becomes a tag with its folder, colour and
  icon, its items get the tag, and a section shows it on Home.
