# Protocol Test

A small Home Assistant add-on that exposes **HTTP**, **HTTPS**, **TCP**, and **TLS-wrapped TCP** listeners on `127.0.0.1`. It is designed to be used together with the [Tailscale](https://github.com/hassio-addons/app-tailscale) add-on to exercise every supported [Tailscale Service](https://tailscale.com/docs/features/tailscale-services) protocol.

## What it does

The add-on starts a single Go binary that listens on four local ports:

| Protocol | Port | Purpose |
|---|---|---|
| HTTP | `8090` | Plain HTTP backend |
| HTTPS | `8091` | TLS backend with a self-signed certificate for `127.0.0.1` |
| TCP | `8092` | Plain TCP echo backend |
| TLS-TCP | `8093` | TLS-wrapped TCP echo backend |

The HTTP/HTTPS handlers return a small JSON payload. The TCP and TLS-TCP listeners echo every line they receive prefixed with `ECHO: `.

A CLI client (`client/`) is included so you can verify all four protocols once the corresponding Tailscale Services are configured and approved in your tailnet.

## Installation

1. In Home Assistant, go to **Settings → Add-ons → Add-on Store**.
2. Click the three-dot menu → **Repositories**.
3. Add `https://codeberg.org/apfohl/home-assistant-apps`.
4. Find **Protocol Test** in the list and click **Install**.
5. Start the add-on.

## Usage

See [`DOCS.md`](DOCS.md) for the full Tailscale Services configuration and CLI client instructions.

## License

MIT © 2026 Andreas Pfohl
