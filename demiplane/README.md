# Home Assistant App: Demiplane WebDAV

A lightweight WebDAV server for Home Assistant OS. Expose directories from your
Home Assistant instance — `/share`, `/media`, `/ssl`, or your app config — over
WebDAV with multi-user authentication and per-directory access control. Designed
to be placed behind a reverse proxy (e.g. nginx-proxy-manager) for HTTPS access
from any WebDAV-capable client.

## Features

- HTTP Basic Authentication with bcrypt-hashed passwords
- Multiple independent users with separate credentials
- Multiple WebDAV shares, each with its own URL prefix and backing directory
- Per-directory user access control lists
- Access to HA `share`, `media`, `ssl`, and app `config` directories
- Single static Go binary — minimal footprint, no runtime dependencies
- Fully configurable through the Home Assistant app options UI

## Supported architectures

![Supports aarch64 Architecture][aarch64-shield]
![Supports amd64 Architecture][amd64-shield]

## Documentation

See [DOCS.md][docs] for full configuration reference, nginx-proxy-manager setup,
WebDAV client compatibility, and troubleshooting.

[aarch64-shield]: https://img.shields.io/badge/aarch64-yes-green.svg
[amd64-shield]: https://img.shields.io/badge/amd64-yes-green.svg
[docs]: https://codeberg.org/apfohl/home-assistant-apps/src/branch/main/demiplane/DOCS.md
