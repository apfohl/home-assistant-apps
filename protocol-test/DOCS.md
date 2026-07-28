# Protocol Test — Documentation

## Overview

Protocol Test is a minimal backend add-on for exercising all four Tailscale Services protocols:

- `http`
- `https`
- `tcp`
- `tls-terminated-tcp`

It runs on the Home Assistant host network and binds to `127.0.0.1` by default, so the existing Tailscale add-on can reach it at the required `127.0.0.1` targets.

## Ports

| Port | Listener | Tailscale service protocol |
|---|---|---|
| `8090` | HTTP | `http` (and optionally `https`) |
| `8091` | HTTPS | `https` |
| `8092` | TCP echo | `tcp` and `tls-terminated-tcp` |
| `8093` | TLS-wrapped TCP echo | direct/local testing only |

> The `tls-terminated-tcp` Tailscale service **terminates TLS on the Tailscale side** and forwards plaintext TCP to the backend. That is why it must point at the plain TCP listener on port `8092`, not the TLS-wrapped listener on port `8093`. Port `8093` is only useful for direct local tests such as `openssl s_client -connect 127.0.0.1:8093`.

## Prerequisites in the Tailscale admin console

Before you can use the `https` or `tls-terminated-tcp` protocols, you must enable **MagicDNS** and **HTTPS certificates** for your tailnet:

1. Open the [Tailscale admin console](https://login.tailscale.com/admin).
2. Go to the **DNS** page.
3. Enable **MagicDNS** if it is not already on.
4. Under **HTTPS Certificates**, click **Enable HTTPS**.

Without this, the Tailscale add-on will log `Tailscale's HTTPS support is disabled` and refuse to advertise any `https` or `tls-terminated-tcp` services.

The Home Assistant device running the Tailscale add-on must also use a [tag-based identity](https://tailscale.com/docs/features/tags), because Tailscale Services cannot be hosted by user-authenticated devices.

## Configuring the Tailscale app

After Protocol Test is running and the prerequisites above are met, configure the Tailscale add-on with the following `services` entries.

```yaml
tags:
  - tag:protocol-test
services:
  - name: svc:http-test
    target: http://127.0.0.1:8090
    protocol: http
    port: 80
    path: /
  - name: svc:https-test
    target: http://127.0.0.1:8090
    protocol: https
    port: 443
    path: /
  - name: svc:tcp-test
    target: tcp://127.0.0.1:8092
    protocol: tcp
    port: 10000
  - name: svc:tls-tcp-test
    target: tcp://127.0.0.1:8092
    protocol: tls-terminated-tcp
    port: 8443
```

> The `https` service uses the plain HTTP backend on port `8090`. Tailscale still serves it with a real Tailscale TLS certificate, so clients see HTTPS. The local HTTPS listener on port `8091` is kept for direct testing only (e.g. `curl -k https://127.0.0.1:8091/`).

After saving, approve the service host in the Tailscale admin console.

## How `protocol` and `target` relate

The Tailscale service `protocol` describes how the service is exposed **on the tailnet** (what the client sees). The `target` describes how Tailscale connects to the **local upstream** service. They are independent:

| Tailscale `protocol` | Client sees | Allowed `target` schemes | What Tailscale does to the backend |
|---|---|---|---|
| `http` | `http://service.tailnet.ts.net` | `http://` | Forwards plain HTTP |
| `https` | `https://service.tailnet.ts.net` | `http://` or `https://` | Terminates TLS at the edge, then forwards HTTP or HTTPS |
| `tcp` | Raw TCP | `tcp://` | Forwards raw TCP unchanged |
| `tls-terminated-tcp` | TLS over TCP | `tcp://` only | Terminates TLS at the edge, then forwards **plaintext TCP** |

That is why the example above uses `protocol: https` with `target: http://127.0.0.1:8090`, and `protocol: tls-terminated-tcp` with `target: tcp://127.0.0.1:8092`.

## Testing the local listeners

From the Home Assistant host or any container with host network access:

```shell
# HTTP
curl http://127.0.0.1:8090/

# HTTPS (accept the self-signed cert)
curl -k https://127.0.0.1:8091/

# TCP echo
printf 'ping\n' | nc 127.0.0.1 8092

# TLS-wrapped TCP echo
printf 'ping\n' | openssl s_client -quiet -connect 127.0.0.1:8093
```

## Testing the Tailscale Services

The CLI client is in the `client/` directory of this repository.

### Build

```shell
cd client
go build -o protocol-test-client .
```

### Run

```shell
./protocol-test-client --tailnet=tail1234.ts.net
```

Replace `tail1234.ts.net` with your actual tailnet name.

The client will connect to each service and print a pass/fail summary:

```text
http-test.tail1234.ts.net:80      HTTP      PASS
https-test.tail1234.ts.net:443    HTTPS     PASS
tcp-test.tail1234.ts.net:10000    TCP       PASS
tls-tcp-test.tail1234.ts.net:8443 TLS-TCP   PASS
```

If any check fails, the client exits with a non-zero status code.

> When testing against the local listeners directly (for example on `localhost` with the self-signed certificate), add `--insecure` to skip TLS verification.

### Flags

| Flag | Default | Description |
|---|---|---|
| `--tailnet` | required | Your tailnet name, e.g. `tail1234.ts.net` |
| `--http-service` | `http-test` | Service name for the HTTP test |
| `--https-service` | `https-test` | Service name for the HTTPS test |
| `--tcp-service` | `tcp-test` | Service name for the TCP test |
| `--tls-tcp-service` | `tls-tcp-test` | Service name for the TLS-terminated TCP test |
| `--http-port` | `80` | Port for the HTTP service |
| `--https-port` | `443` | Port for the HTTPS service |
| `--tcp-port` | `10000` | Port for the TCP service |
| `--tls-tcp-port` | `8443` | Port for the TLS-terminated TCP service |
| `--timeout` | `10s` | Per-test timeout |
| `--insecure` | `false` | Skip TLS certificate verification (useful for local testing) |

## Environment variables

The add-on can be reconfigured with these environment variables (all are optional):

| Variable | Default | Description |
|---|---|---|
| `LISTEN_HOST` | `127.0.0.1` | Bind address |
| `HTTP_PORT` | `8090` | HTTP listener port |
| `HTTPS_PORT` | `8091` | HTTPS listener port |
| `TCP_PORT` | `8092` | TCP echo port |
| `TLS_TCP_PORT` | `8093` | TLS-wrapped TCP echo port |
| `CERT_DIR` | `/data/certs` | Where the self-signed certificate is stored |

## Security

- The add-on runs with `host_network: true` so it can bind to `127.0.0.1` and be reached by the Tailscale add-on.
- The certificate generated on first start is a self-signed certificate for `127.0.0.1` and `localhost` only.
- The add-on does not call the Supervisor API or require any Home Assistant authentication.

## Troubleshooting

**`Tailscale's HTTPS support is disabled`**

You are trying to advertise an `https` or `tls-terminated-tcp` service, but MagicDNS and HTTPS certificates are not enabled for your tailnet. Enable them in the Tailscale admin console under **DNS → HTTPS Certificates**, then restart the Tailscale add-on.

**Service not reachable**

Make sure the Tailscale service host has been approved in the Tailscale admin console and that the Home Assistant device has a tag.

**HTTPS service returns HTTP 502**

You have the `https` service pointed at the self-signed HTTPS backend (`https://127.0.0.1:8091`). Tailscale’s HTTPS proxy cannot validate that certificate. Change the `https` service target to `http://127.0.0.1:8090` instead. Tailscale will still serve the service with a real TLS certificate, so the client sees HTTPS.

**TLS-terminated TCP test fails**

Verify that the service target is `tcp://127.0.0.1:8092`, not port `8093`. Tailscale handles the TLS termination for this protocol.

## License

MIT © 2026 Andreas Pfohl
