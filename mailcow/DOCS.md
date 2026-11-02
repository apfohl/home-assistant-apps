# Home Assistant App: Mailcow Alias Manager

A Home Assistant add-on for managing mailbox aliases on [hosted mailcow](https://hosted.mailcow.de).
It communicates with mailcow through the web UI scraper — no API token or
alternative access method is used.

## Features

- List all mailbox aliases for the configured domain-admin account
- Create and edit aliases with selectable or custom target addresses
- Delete aliases with a browser confirmation dialog
- Search/filter aliases by alias address or goto addresses
- All configuration is managed through the built-in settings page
- Login role is fixed to `domain_admin`

## How it works

On startup the add-on reads `/data/settings.json`. If credentials are present,
it logs in to hosted mailcow and keeps the session in a cookie jar at
`/data/mailcow-cookie-jar.json`.

The ingress-enabled dashboard on port 8080 provides:

- `/` — alias list with live search, delete and edit actions, and a create button
- `/create` — form for new alias address and goto addresses
- `/edit` — form for updating an existing alias's goto addresses
- `/settings` — credentials and suggested target addresses

## Configuration

All configuration is managed through the built-in settings page. Open the
Mailcow dashboard from the HA sidebar and click the **Settings** tab (or
navigate to `/settings` directly).

Settings are stored in `/data/settings.json` (private to the add-on data
volume) and changes take effect immediately without restarting the add-on.

### Runtime environment

`ENVIRONMENT` controls whether the add-on runs behind Home Assistant ingress or
standalone. It defaults to `production`, so the Home Assistant add-on works
without any extra configuration.

| Value | Description |
|---|---|
| `production` (default) | Runs behind Home Assistant ingress. Enforces the ingress IP (`172.30.32.2`) and reads `X-Ingress-Path`. This is set explicitly by the add-on's s6 service. |
| `development` | Runs standalone. Disables ingress IP enforcement and allows a missing `X-Ingress-Path` header. |

If `ENVIRONMENT` has any other value, the process exits with an error.

### Default paths

| File | Production | Development |
|---|---|---|
| Settings | `/data/settings.json` | `./dev-data/settings.json` |
| Cookie jar | `/data/mailcow-cookie-jar.json` | `./dev-data/mailcow-cookie-jar.json` |

Both paths can be overridden with flags (see below).

### Field reference

| Field | Description |
|---|---|
| `username` | Your hosted mailcow domain-admin username. |
| `password` | Your hosted mailcow domain-admin password. Leave blank when saving to retain the current password. |
| Suggested target addresses | Optional addresses, one per line, offered in the create and edit forms. Custom addresses remain allowed. |

The login role is hardcoded to `domain_admin` and is not configurable.

### Flags

| Flag | Default | Description |
|---|---|---|
| `-settings` | `/data/settings.json` in production, `./dev-data/settings.json` in development | Path to the settings JSON file. |
| `-cookie-file` | `/data/mailcow-cookie-jar.json` in production, `./dev-data/mailcow-cookie-jar.json` in development | Path to the persisted cookie jar file. |

## Creating an alias

1. Open the Mailcow dashboard.
2. Click **Create alias**.
3. Enter the alias address (e.g. `alias@example.com`).
4. Select suggested targets or type addresses and press Enter or comma. Remove targets with the × button.
5. Submit the form. The new alias appears on the dashboard.

New aliases are always created as active.

## Local development

You can run the add-on locally without Home Assistant for development and
testing:

```bash
ENVIRONMENT=development go run .
```

Then open http://localhost:8080/settings, enter your hosted mailcow
credentials, and navigate to http://localhost:8080/ to manage aliases.

To work without a real mailcow endpoint or credentials, start the in-memory mock:

```bash
ENVIRONMENT=development go run . -mock
```

Open http://localhost:8080/ and configure suggested targets under Settings.
The mock starts with an example alias; changes to aliases are lost when the
process stops. Settings are saved in `./dev-data/`. The `-mock` flag is
available only in development mode.

Credentials and session cookies are stored in `./dev-data/` by default. This
directory is ignored by git.

To use custom paths:

```bash
ENVIRONMENT=development go run . \
    -settings /tmp/mailcow-settings.json \
    -cookie-file /tmp/mailcow-cookies.json
```

## Logs

The add-on logs startup, login status, and any errors encountered while
communicating with hosted mailcow to the Home Assistant app log.
