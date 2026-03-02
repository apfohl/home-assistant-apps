# Changelog

All notable changes to Tavern will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [1.1.1] - 2026-03-02

### Fixed

- Disk usage values are already in GB as returned by the Supervisor API;
  removed incorrect `/1024` MB-to-GB conversion that produced nonsensical
  sub-1 GB figures

## [1.1.0] - 2026-03-02

### Fixed

- Uptime was always shown as "unknown" — the `/host/info` API has no `uptime`
  field; uptime is now correctly derived from `boot_timestamp` (microseconds
  since Unix epoch)
- `version_latest` JSON tag on core info was incorrectly mapped as
  `last_version`; latest available HA version now displays correctly

### Added

- Disk usage card showing total, used, and free space with a colour-coded
  progress bar (green < 75%, amber ≥ 75%, red ≥ 90%)
- AppArmor profile fixed: replaced overly-restrictive child-profile `cx`
  transition with flat `ix` (inherit) and added required `/proc` entries for
  the Go runtime

## [1.0.0] - 2026-03-02

### Added

- Initial release
- Go HTTP server (port 8099, ingress-only from `172.30.32.2`)
- Live dashboard displaying ingress headers, authenticated user info,
  HA Core version, Supervisor info, and host system details
- Supervisor REST API integration via `SUPERVISOR_TOKEN`
- HA sidebar link with `mdi:door-open` icon
- Custom AppArmor profile
