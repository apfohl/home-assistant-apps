# Home Assistant App: Chronicle IMAP Backup

A scheduled IMAP backup app for Home Assistant OS. It uses the
[imap-backup](https://github.com/joeyates/imap-backup) Ruby gem for
incremental mail synchronization and wraps it with a pure-Go scheduler,
per-account tar.gz archiving, and retention-based pruning — no shell scripts,
no external cron daemon.

## Features

- Scheduled IMAP sync driven by a standard cron expression
- Multiple independent accounts — a failure on one does not affect others
- Incremental sync: only new and changed messages are downloaded on each run
- Mirror mode: remote deletions are reflected in the local mail store when enabled
- Compressed tar.gz archive created after each successful sync, stored in a
  configurable long-term archive path
- Retention-based pruning: archives older than `retention_days` days are removed
  automatically after each backup run
- Per-account mail stores auto-named from the sanitized username
  (e.g. `user@example.com` → `user_at_example_com`)
- Fully configurable through the built-in settings page — no HA Supervisor UI required

## How it works

On each scheduled run, Chronicle processes accounts sequentially:

1. Generates a temporary per-account `config.json` for `imap-backup` and
   deletes it after use — credentials are never persisted to disk by Chronicle
   itself.
2. Runs `imap-backup backup` for the account. stdout and stderr are forwarded
   to the HA log.
3. On success: creates a `chronicle-<username>-<timestamp>.tar.gz` archive of
   the account's mail store directory.
4. Prunes archives for that account that are older than `retention_days` days.

If a step fails, the error is logged and Chronicle moves on to the next account.
The scheduler continues running and retries on the next scheduled tick.

## Configuration

All configuration is managed through the built-in settings page. Open the
Chronicle dashboard from the HA sidebar and click the **Settings** tab (or
navigate to `/settings` directly).

Settings are stored in `/data/settings.json` (private to the add-on data
volume) and changes take effect immediately without restarting the add-on.

### Field reference

| Field | Default | Description |
|---|---|---|
| `cron_expression` | `0 3 * * *` | Standard 5-field cron expression controlling when backups run. Examples: `0 3 * * *` (daily at 03:00), `0 */6 * * *` (every 6 hours), `0 2 * * 0` (weekly on Sunday at 02:00). |
| `backup_base_path` | `/share/chronicle/data` | Root directory for incremental IMAP sync data. Each account gets a subdirectory named after its sanitized username (e.g. `user_at_example_com`). Created automatically if it does not exist. |
| `archive_path` | `/share/chronicle/archives` | Directory where compressed tar.gz archives are written after each sync. Created automatically if it does not exist. |
| `retention_days` | `5` | Number of days to keep archives. Archives older than this value are deleted after each backup run. |
| `verbose` | `false` | When enabled, passes `-v` to `imap-backup backup` for detailed debug output in the add-on logs. |

#### Per-account fields

| Field | Description |
|---|---|
| `username` | IMAP login name — usually the full email address (e.g. `user@example.com`). |
| `password` | IMAP account password. Stored in `/data/settings.json` (private to the add-on volume). |
| `server` | Hostname of the IMAP server (e.g. `imap.example.com` or `imap.gmail.com`). |
| `mirror_mode` | When `true`, messages deleted on the server are also deleted locally. |
| `multi_fetch_size` | Number of messages fetched per IMAP request. Reduce to `1` for rate-limited servers. |

## Storage layout

```
/share/chronicle/
├── data/
│   ├── alice_at_example_com/    ← incremental sync (imap-backup mailboxes)
│   └── bob_at_another_org/
└── archives/
    ├── chronicle-alice_at_example_com-2026-01-15T03-00-01.tar.gz
    ├── chronicle-alice_at_example_com-2026-01-16T03-00-04.tar.gz
    └── chronicle-bob_at_another_org-2026-01-15T03-01-12.tar.gz
```

## Logs

Chronicle logs all activity to the HA app log, including:

- Scheduler startup and next scheduled run time
- Per-account backup job start and completion
- `imap-backup` stdout/stderr output (forwarded verbatim)
- Archive creation paths and pruned file names
- Any errors encountered (with the account name as context)
