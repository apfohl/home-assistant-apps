# Changelog

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
