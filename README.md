# Home Assistant Apps Repository

Home Assistant app repository providing a lightweight WebDAV server, a
scheduled IMAP backup tool, and an ingress gateway dashboard.

## Apps

### [Demiplane WebDAV](./demiplane)

A WebDAV server with multi-user basic auth and per-directory access control.
Designed to be exposed publicly via a reverse proxy such as nginx-proxy-manager.

- Multi-user authentication with bcrypt-hashed passwords
- Per-directory access control lists
- Access to Home Assistant shared and media directories
- HTTP server suitable for TLS termination by a reverse proxy

### [Chronicle IMAP Backup](./chronicle)

A scheduled IMAP backup app that syncs mail accounts and creates long-term
compressed archives with configurable retention.

- Scheduled IMAP sync via a standard cron expression
- Multiple independent accounts, each isolated from failures in others
- Compressed tar.gz archives created after each successful sync
- Retention-based archive pruning
- Pure-Go scheduler — no shell scripts or external cron daemon

### [Tavern](./tavern)

A small ingress gateway dashboard that explores the Home Assistant ingress
feature. Opens directly in the HA sidebar — no port forwarding or separate
login required.

- Live dashboard showing HA Core, Supervisor, and host system information
- Displays all ingress-injected headers including authenticated user details
- Connects exclusively through the ingress gateway (`172.30.32.2`)
- Demonstrates `ingress: true`, `panel_icon`, `hassio_api`, and custom AppArmor

## Installation

Add this repository to your Home Assistant instance:

1. Go to **Settings** → **Apps** → **App Store**
2. Click the three-dot menu in the top right and select **Repositories**
3. Add `https://codeberg.org/apfohl/home-assistant-apps` and click **Add**

## License

[MIT](./LICENSE) — Andreas Pfohl
