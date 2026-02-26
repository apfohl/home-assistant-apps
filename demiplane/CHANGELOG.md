## [Unreleased]

## [1.1.0] - 2026-02-26

- Migrated service management from legacy s6-overlay `services.d` to native s6-rc.d layout

## [1.0.0] - 2026-02-23

### Added

- Initial release
- Multi-user HTTP Basic Authentication with bcrypt password hashing
- Per-directory user access control lists
- Read/write access to Home Assistant share, media, ssl, and app config directories
- Configurable entirely through the Home Assistant app options UI
- Suitable for exposure via a reverse proxy (e.g., nginx-proxy-manager) over HTTPS
