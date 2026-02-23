# Home Assistant Apps Repository

Home Assistant app repository providing a lightweight WebDAV server.

## Apps

### [Demiplane WebDAV](./demiplane)

A WebDAV server with multi-user basic auth and per-directory access control.
Designed to be exposed publicly via a reverse proxy such as nginx-proxy-manager.

- Multi-user authentication with bcrypt-hashed passwords
- Per-directory access control lists
- Access to Home Assistant shared and media directories
- HTTP server suitable for TLS termination by a reverse proxy

## Installation

Add this repository to your Home Assistant instance:

1. Go to **Settings** → **Apps** → **App Store**
2. Click the three-dot menu in the top right and select **Repositories**
3. Add `https://codeberg.org/apfohl/home-assistant-apps` and click **Add**

## License

[MIT](./LICENSE) — Andreas Pfohl
