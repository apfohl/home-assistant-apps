# Home Assistant Add-on: Demiplane WebDAV

A lightweight WebDAV server that integrates with the Home Assistant OS add-on
ecosystem. It provides authenticated, multi-user WebDAV access to directories
on your Home Assistant instance and is designed to be exposed publicly via a
reverse proxy such as nginx-proxy-manager.

## Features

- HTTP Basic Authentication with bcrypt-hashed passwords (hashed in memory at
  startup — plaintext passwords are never stored by the server process)
- Multiple users with independent credentials
- Multiple WebDAV directories, each with a configurable URL prefix, backing OS
  path, and optional per-directory user access control list
- Access to the Home Assistant `/share`, `/media`, `/ssl`, and `/config`
  (add-on config) directories
- Plain HTTP — TLS termination is handled by your reverse proxy

## Configuration

All configuration is done through the Home Assistant add-on options UI.

### `server_port`

The TCP port the WebDAV server listens on inside the container. Default: `8080`.

You must also map this port on the **Network** tab of the add-on so that it is
reachable from the host. Use this host port as the upstream address in
nginx-proxy-manager.

### `users`

A list of user objects. Each entry requires:

| Field      | Description                                 |
|------------|---------------------------------------------|
| `username` | The login name for this user.               |
| `password` | The password (entered as plaintext in the UI; stored in options.json but bcrypt-hashed in memory at startup). |

Example:

```yaml
users:
  - username: alice
    password: "a very strong passphrase"
  - username: bob
    password: "another good passphrase"
```

### `directories`

A list of directory share objects. Each entry requires:

| Field           | Description                                                                                              |
|-----------------|----------------------------------------------------------------------------------------------------------|
| `name`          | A human-readable label shown in logs.                                                                    |
| `prefix`        | The URL path prefix at which this share is mounted, e.g. `/files/docs`.                                  |
| `os_path`       | The absolute path inside the container, e.g. `/share/documents`.                                         |
| `allowed_users` | Optional list of usernames allowed to access this share. Empty list means all authenticated users. |

Available base paths inside the container:

| Path       | Home Assistant source          |
|------------|-------------------------------|
| `/share/`  | HA shared directory            |
| `/media/`  | HA media directory             |
| `/ssl/`    | HA SSL certificates (read-only by default) |
| `/config/` | This add-on's config directory |

Example:

```yaml
directories:
  - name: "Public Docs"
    prefix: "/files/docs"
    os_path: "/share/documents"
    allowed_users: []

  - name: "Alice Private"
    prefix: "/files/alice"
    os_path: "/share/alice"
    allowed_users:
      - alice

  - name: "Team Shared"
    prefix: "/files/team"
    os_path: "/share/team"
    allowed_users:
      - alice
      - bob
```

## Exposing via nginx-proxy-manager

1. Start the add-on and note the host port (default `8080`).
2. In nginx-proxy-manager, create a new **Proxy Host**:
   - **Domain names**: your public domain, e.g. `dav.example.com`
   - **Scheme**: `http`
   - **Forward hostname / IP**: the IP address of your Home Assistant instance
   - **Forward port**: the host port mapped above (e.g. `8080`)
   - Enable **SSL** on the SSL tab and request a Let's Encrypt certificate
3. WebDAV clients connect to `https://dav.example.com/files/docs` (or
   whichever prefix you configured) with their username and password.

## WebDAV client compatibility

Most operating systems and applications support WebDAV natively:

- **macOS**: Finder → Go → Connect to Server → `https://dav.example.com/files/docs`
- **Windows**: Map Network Drive → `https://dav.example.com/files/docs`
- **Linux**: `davfs2`, Nautilus (Files), or any WebDAV-capable client
- **iOS / Android**: Documents by Readdle, FX File Explorer, Solid Explorer, etc.

## Advanced: generating a bcrypt hash manually

The add-on binary includes a helper sub-command for generating bcrypt hashes
outside of the HA UI (useful for scripting or debugging):

```sh
docker exec -it addon_local_demiplane /usr/bin/demiplane -hash-password "mypassword"
```

This prints a bcrypt hash to stdout and exits immediately.
