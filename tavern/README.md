# Tavern

A small Home Assistant app that explores the **ingress** feature. Once installed, Tavern appears as a sidebar link in
the Home Assistant UI and renders a live system information dashboard — served directly through the HA ingress gateway,
with no port forwarding or separate authentication required.

## What it does

Tavern runs a minimal Go web server (port 8099, ingress-only) that:

- Accepts connections exclusively from the HA ingress gateway (`172.30.32.2`)
- Reads ingress-injected headers (`X-Ingress-Path`, `X-Remote-User-*`) and displays them
- Calls the Supervisor REST API to fetch live data about your HA installation
- Renders a dark-themed dashboard showing:
    - Ingress gateway status and path
    - Authenticated user details (provided by HA — no separate login needed)
    - Home Assistant Core version and state
    - Supervisor version and channel
    - Host system information (hostname, OS, kernel, uptime)

## Installation

1. In Home Assistant, go to **Settings → Add-ons → Add-on Store**
2. Click the three-dot menu → **Repositories**
3. Add `https://codeberg.org/apfohl/home-assistant-apps`
4. Find **Tavern** in the list and click **Install**
5. Start the add-on — a **Tavern** link will appear in the sidebar automatically

## Why "Tavern"?

In D&D, the tavern is the gateway where every adventure begins — the first place you pass through before entering the
wider world. This app is the gateway that demonstrates how HA ingress works: a door that HA holds open for you, already
authenticated.

## License

MIT © 2026 Andreas Pfohl
