# Changelog

All notable changes to Demiplane will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [1.1.2] - 2026-03-01

### Fixed

- Use `errors.Is` instead of `==` for `http.ErrServerClosed` check (idiomatic Go error comparison)
- Call `signal.Stop` after receiving the shutdown signal to clean up the signal channel
- Log shutdown errors with `Printf` instead of `Fatalf` so a drain/timeout error doesn't obscure that shutdown completed

## [1.1.1] - 2026-03-01

### Added

- Implemented graceful shutdown: the HTTP server listens for SIGTERM/SIGINT and performs a clean shutdown with a 10-second timeout for in-flight requests

## [1.1.0] - 2026-02-26

### Changed

- Migrated service management from legacy s6-overlay `services.d` to native s6-rc.d layout

## [1.0.0] - 2026-02-22

### Added

- Initial release
- Multi-user HTTP Basic Authentication with bcrypt password hashing
- Per-directory user access control lists
- Read/write access to Home Assistant share, media, ssl, and app config directories
- Configurable entirely through the Home Assistant app options UI
- Suitable for exposure via a reverse proxy (e.g., nginx-proxy-manager) over HTTPS
