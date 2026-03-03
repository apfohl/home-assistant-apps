# Home Assistant App: Chronicle IMAP Backup

A scheduled IMAP backup app for Home Assistant OS. It uses the
[imap-backup](https://github.com/joeyates/imap-backup) Ruby gem for
incremental mail synchronisation and wraps it with a pure-Go scheduler,
per-account tar.gz archiving, and retention-based pruning — no shell scripts,
no external cron daemon.

## Features

- Scheduled IMAP sync via a standard cron expression
- Multiple independent accounts, each processed in isolation
- Per-account mail store under `/share/chronicle/data/<username>/`
- Compressed tar.gz archives created after each successful sync
- Configurable archive retention: archives older than N days are pruned
  automatically
- Mirror mode support: remote deletions can be reflected locally
- Pure-Go scheduler — replaces crond with a self-contained binary
- Fully configurable through the built-in settings dashboard (no HA Supervisor options required)

## Supported architectures

![Supports aarch64 Architecture][aarch64-shield]
![Supports amd64 Architecture][amd64-shield]

## Documentation

See [DOCS.md][docs] for full configuration reference and usage details.

[aarch64-shield]: https://img.shields.io/badge/aarch64-yes-green.svg
[amd64-shield]: https://img.shields.io/badge/amd64-yes-green.svg
[docs]: https://codeberg.org/apfohl/home-assistant-apps/src/branch/main/chronicle/DOCS.md
