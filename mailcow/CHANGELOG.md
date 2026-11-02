# Changelog

All notable changes to Mailcow Alias Manager will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [1.3.0] - 2026-11-02

### Added

- Configure suggested target addresses and select or enter targets as removable tags when creating or editing aliases
- Run a standalone development server with an in-memory mock mailcow

### Fixed

- Keep the existing password when saving settings with a blank password field

## [1.2.1] - 2026-11-02

### Fixed

- Submit the current hosted Mailcow CSRF token for alias operations and preserve alias settings when editing
- Report failed or unconfirmed alias operations rather than silently treating them as successful

## [1.2.0] - 2026-07-14

### Added

- Edit alias action on the dashboard to update goto addresses
- Edit page for updating an existing alias's destination addresses

## [1.1.0] - 2026-07-13

### Added

- Live search/filter bar on the alias dashboard
- Filter matches against alias address and goto addresses
- Clear button to reset the search field

## [1.0.0] - 2026-07-13

### Added

- Initial release
- List mailbox aliases scraped from hosted mailcow
- Create aliases with comma-separated goto addresses
- Delete aliases with confirmation
- Settings page for username and password
- Role hardcoded to `domain_admin`
- Ingress-enabled web dashboard on port 8080
