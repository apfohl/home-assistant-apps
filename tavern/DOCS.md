# Tavern — Documentation

## Overview

Tavern is a minimal ingress test app for Home Assistant. It has no user-configurable options — everything is handled
automatically by the HA ingress gateway.

Once the add-on is started, click the **Tavern** link in the Home Assistant sidebar to open the dashboard.

## How HA Ingress works

The ingress feature routes traffic from the HA frontend directly into the add-on container, via the internal gateway at
`172.30.32.2`. This means:

- **No port forwarding** is needed — the add-on's HTTP server is never exposed to the network
- **Authentication is handled by Home Assistant** — the user is already logged in when they click the sidebar link
- **HA injects request headers** that identify the authenticated user and provide routing context:

| Header                       | Description                                                                         |
|------------------------------|-------------------------------------------------------------------------------------|
| `X-Ingress-Path`             | The path prefix HA uses to route to this add-on (e.g. `/api/hassio_ingress/abc123`) |
| `X-Remote-User-Id`           | The HA user's internal UUID                                                         |
| `X-Remote-User-Name`         | The HA user's username                                                              |
| `X-Remote-User-Display-Name` | The HA user's display name                                                          |

Tavern displays all of these on the dashboard so you can see exactly what ingress provides.

## Supervisor API access

Tavern uses the `SUPERVISOR_TOKEN` environment variable (automatically injected by HA) to call the local Supervisor REST
API:

| Endpoint                            | Data shown                                         |
|-------------------------------------|----------------------------------------------------|
| `http://supervisor/supervisor/info` | Supervisor version, channel, architecture          |
| `http://supervisor/core/info`       | HA Core version, latest available version, state   |
| `http://supervisor/host/info`       | Hostname, operating system, kernel version, uptime |

This is enabled by `hassio_api: true` in the add-on configuration.

## Security

Tavern achieves the maximum HA security rating:

- **+2** from `ingress: true` — ingress is the most secure way to expose a web UI
- **+1** from `apparmor: true` with a custom `apparmor.txt` profile (applied after installation)
- Base score of 5 → effective score: **6/6**

The Go server enforces the ingress requirement by rejecting any connection that does not originate from `172.30.32.2`.

## Troubleshooting

**Dashboard shows "SUPERVISOR_TOKEN not set"**
This means the add-on is not running inside Home Assistant (e.g. you are running it locally for development). The page
will still render but system info fields will be empty.

**Supervisor API errors**
If individual API calls fail, an error banner appears at the top of the dashboard with details. This can happen if the
Supervisor is temporarily unavailable or if the `hassio_api` permission was not granted.

**Sidebar link does not appear**
Ensure the add-on is started. The sidebar link is only shown when the add-on is running.
