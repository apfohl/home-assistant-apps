# Changelog

## 1.5.0

- Removed `options:` and `schema:` blocks from `config.yaml` — Chronicle no longer uses the HA Supervisor options UI for configuration
- All configuration is now managed exclusively via the dashboard `/settings` page (`/data/settings.json`)
- Removed migration code (`loadOptions`, `migrateFromOptions`) and the `-options` CLI flag — the options.json migration path from 1.4.0 is no longer needed
- Fresh installs (no `settings.json` and no `options.json`) now start normally with hardcoded defaults and an empty account list; the user is prompted to open `/settings` to add accounts
- Version bumped to `1.5.0`

## 1.4.0

- Added dashboard settings page (`/settings`) for managing all configuration without restarting the add-on
- Settings are stored in `/data/settings.json` (Chronicle-owned, private to the add-on volume) — not in the HA Supervisor options file
- On first start, settings are auto-migrated from `/data/options.json` so existing users are not disrupted; the options file is left untouched (Phase 1)
- Settings changes are applied immediately (hot-reload): cron schedule is rescheduled and in-memory state is updated under a `sync.RWMutex` without restarting the process
- Settings form covers all fields: cron expression, backup base path, archive path, retention days, verbose logging, and full IMAP account management (add / edit / remove accounts including passwords)
- Server-side validation for all fields including cron expression parsing
- Atomic write for `settings.json` (write to temp file → fsync → rename) to prevent corruption on crash
- Archive dashboard and settings page share consistent nav tabs in the header

## 1.3.1

- Changed dashboard header icon to 🗃️ emoji
- Responsive table layout: at ≤ 600 px the archive table reflows into stacked labelled rows so no columns are clipped on narrow viewports

## 1.3.0

- Added ingress-enabled web dashboard accessible from the HA sidebar as "Chronicle" (`mdi:email-sync` icon)
- Dashboard lists all backup archives grouped by account, sorted newest-first, in vertically-stacked cards
- Each archive entry shows filename, file size, and creation timestamp
- Download action streams the `.tar.gz` archive directly as an attachment
- Delete action removes an archive after a browser confirmation dialog
- Dark grey and yellow color scheme; footer matches Tavern style
- HTTP server runs on port 8080 in the same process as the cron scheduler, with graceful shutdown on SIGTERM/SIGINT

## 1.2.2

- Propagate context cancellation into `runImapBackup` and `backupAccount` via `context.WithCancel`, so that a SIGTERM/SIGINT immediately cancels any in-progress `imap-backup` subprocess

## 1.2.1

- Implemented graceful shutdown: listens for SIGTERM/SIGINT and waits for the running cron job to complete before exiting

## 1.2.0

- Migrated service management from legacy s6-overlay `services.d` to native s6-rc.d layout

## 1.1.0

- Added `verbose` option: when enabled, passes `-v` to the `imap-backup backup` command for detailed debug output in the add-on logs

## 1.0.0

- Initial release
- Go-based cron scheduler
- Per-account imap-backup config.json generated at runtime
- Pure-Go tar.gz archiving after each successful sync
- Configurable retention-based archive pruning
