# Changelog

All notable changes to Tavern will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [1.0.0] - 2026-03-02

### Added

- Initial release
- Go HTTP server (port 8099, ingress-only from `172.30.32.2`)
- Live dashboard displaying ingress headers, authenticated user info,
  HA Core version, Supervisor info, and host system details
- Supervisor REST API integration via `SUPERVISOR_TOKEN`
- HA sidebar link with `mdi:door-open` icon
- Custom AppArmor profile
