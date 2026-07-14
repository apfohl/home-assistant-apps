# Home Assistant App: Mailcow Alias Manager

A Home Assistant add-on for managing mailbox aliases on [hosted mailcow](https://hosted.mailcow.de).
It logs in as a domain admin through the web UI scraper and lets you list,
create, and delete aliases from the Home Assistant sidebar.

## Features

- List all mailbox aliases
- Create new aliases with one or more comma-separated goto addresses
- Edit existing aliases to update their goto addresses
- Delete aliases with a confirmation prompt
- Search/filter aliases by alias address or goto addresses
- Credentials managed through the built-in settings page
- Role fixed to `domain_admin`

## Supported architectures

![Supports aarch64 Architecture][aarch64-shield]
![Supports amd64 Architecture][amd64-shield]

## Documentation

See [DOCS.md][docs] for full configuration reference and usage details.

[aarch64-shield]: https://img.shields.io/badge/aarch64-yes-green.svg
[amd64-shield]: https://img.shields.io/badge/amd64-yes-green.svg
[docs]: https://codeberg.org/apfohl/home-assistant-apps/src/branch/main/mailcow/DOCS.md
