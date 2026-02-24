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
- Fully configurable through the Home Assistant app options UI

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

All configuration is done through the Home Assistant app options UI.

### `cron_expression`

Standard 5-field cron expression controlling when backups run. Default: `0 3 * * *`.

| Example | Meaning |
|---|---|
| `0 3 * * *` | Daily at 03:00 |
| `0 */6 * * *` | Every 6 hours |
| `0 2 * * 0` | Weekly on Sunday at 02:00 |

### `backup_base_path`

Root directory for incremental IMAP sync data. Default: `/share/chronicle/data`.

Each account gets a subdirectory derived from its sanitized username:

| Account | Local path |
|---|---|
| `user@example.com` | `/share/chronicle/data/user_at_example_com` |
| `alice@mail.org` | `/share/chronicle/data/alice_at_mail_org` |

The directory is created automatically if it does not exist.

### `archive_path`

Directory where compressed tar.gz archives are written after each sync.
Default: `/share/chronicle/archives`.

Archive filenames follow the pattern:

```
chronicle-<sanitized_username>-<timestamp>.tar.gz
```

For example: `chronicle-user_at_example_com-2026-01-15T03-00-01.tar.gz`.

The directory is created automatically if it does not exist.

### `retention_days`

Number of days to keep archives. Archives older than this value are deleted
after each backup run. Default: `5`.

### `accounts`

A list of IMAP account objects. Each entry requires:

| Field | Description |
|---|---|
| `username` | IMAP login name — usually the full email address |
| `password` | IMAP password (stored in options.json; handled by HA Supervisor) |
| `server` | IMAP server hostname (e.g. `imap.example.com`) |
| `mirror_mode` | When `true`, messages deleted on the server are also deleted locally |
| `multi_fetch_size` | Number of messages fetched per IMAP request; reduce to `1` for rate-limited servers |

## Example configuration

```yaml
cron_expression: "0 3 * * *"
backup_base_path: /share/chronicle/data
archive_path: /share/chronicle/archives
retention_days: 5
accounts:
  - username: alice@example.com
    password: secret
    server: imap.example.com
    mirror_mode: true
    multi_fetch_size: 1
  - username: bob@another.org
    password: hunter2
    server: mail.another.org
    mirror_mode: false
    multi_fetch_size: 5
```

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
