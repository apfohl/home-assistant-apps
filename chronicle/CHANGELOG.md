# Changelog

## 1.1.0

- Added `verbose` option: when enabled, passes `-v` to the `imap-backup backup` command for detailed debug output in the add-on logs

## 1.0.0

- Initial release
- Go-based cron scheduler
- Per-account imap-backup config.json generated at runtime
- Pure-Go tar.gz archiving after each successful sync
- Configurable retention-based archive pruning
