[English](/README.md) | [فارسی](/README.fa_IR.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/p-ui-dark.png">
    <img alt="Penhoon UI" src="./media/p-ui-light.png" width="320">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/Arman2122/p-ui/releases"><img src="https://img.shields.io/github/v/release/Arman2122/p-ui" alt="Release"></a>
  <a href="https://github.com/Arman2122/p-ui/actions"><img src="https://img.shields.io/github/actions/workflow/status/Arman2122/p-ui/release.yml.svg" alt="Build"></a>
  <a href="#"><img src="https://img.shields.io/github/go-mod/go-version/Arman2122/p-ui.svg" alt="Go Version"></a>
  <a href="https://www.gnu.org/licenses/gpl-3.0.en.html"><img src="https://img.shields.io/badge/license-GPL%20V3-blue.svg?longCache=true" alt="License"></a>
</p>

# Penhoon UI

**Penhoon UI** (`p-ui`) is an open-source web control panel for managing
[Xray-core](https://github.com/XTLS/Xray-core) servers — deploying, configuring and
monitoring proxy and VPN protocols from a single VPS up to multi-node deployments.

> **Penhoon UI is a fork of [3x-ui](https://github.com/MHSanaei/3x-ui) by
> [MHSanaei](https://github.com/MHSanaei).** Everything that works here works because of
> that project. Penhoon UI keeps the same GPL-3.0 licence and full upstream commit history,
> and diverges from it to pursue the roadmap below.

> [!IMPORTANT]
> This project is intended for personal use. Please do not use it for illegal purposes
> or in a production environment.

## Project Direction

Penhoon UI is not trying to be a drop-in copy of its upstream. Two goals drive the work:

- **Multi-protocol VPN support beyond Xray.** Xray-core covers the proxy side well, but a
  VPN panel should not be limited to one engine. The plan is to manage additional VPN
  backends — WireGuard, OpenVPN, IKEv2/IPsec and others — from the same panel, with one
  consistent model for clients, quotas, expiry and subscriptions regardless of which
  protocol serves them.
- **Finish the Xray integration properly.** Bring the existing Xray support to full
  coverage of the core's feature set — every inbound and outbound, every transport,
  complete routing and DNS control, and configuration surfaced in the UI instead of
  requiring hand-edited JSON.

Work is ongoing and the roadmap will move. Issues and pull requests are welcome.

## Features

- **Multi-protocol inbounds** — VLESS, VMess, Trojan, Shadowsocks, WireGuard, Hysteria2,
  HTTP, SOCKS (Mixed), Dokodemo-door / Tunnel, and TUN.
- **Modern transports & security** — TCP (Raw), mKCP, WebSocket, gRPC, HTTPUpgrade and
  XHTTP, secured with TLS, XTLS and REALITY.
- **Fallbacks** — serve multiple protocols on a single port (e.g. VLESS and Trojan on 443).
- **Per-client management** — traffic quotas, expiry dates, IP limits, live online status,
  one-click share links, QR codes and subscriptions.
- **Traffic statistics** — per inbound, per client and per outbound, with reset controls.
- **Multi-node support** — manage and scale across multiple servers from one panel.
- **Outbound & routing** — WARP, NordVPN, custom routing rules, load balancers and
  outbound proxy chaining.
- **Built-in subscription server** with multiple output formats and
  [custom page templates](docs/custom-subscription-templates.md).
- **Telegram bot** for remote monitoring and management.
- **RESTful API** with in-panel Swagger documentation.
- **PostgreSQL storage** — a single, well-understood backend with `pg_dump` /
  `pg_restore` backups.
- **Fail2ban integration** for enforcing per-client IP limits.
- **English and Persian UI** with dark and light themes.

## Quick Start

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)
```

Install a specific version by appending its tag:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh) v1.0.0
```

The installer sets up PostgreSQL (or asks for the DSN of an existing server) and generates
a random username, password and access path. Afterwards run `p-ui` to open the management
menu, where you can start and stop the service, view or reset your credentials, manage SSL
certificates and more.

### Unattended install

The installer also runs non-interactively, for cloud-init and other automation. Set
`PUI_NONINTERACTIVE=1` (or pipe it with no TTY) and it installs end to end with no prompts,
generating random credentials and writing them to `/etc/p-ui/install-result.env`.

See [`deploy/`](deploy/) for [cloud-init user-data](deploy/cloud-init/) and
[Hetzner Cloud notes](deploy/marketplace/hetzner/).

## Supported Platforms

**Operating systems:** Ubuntu 22.04, 24.04 and 26.04, and Debian 12 or newer. Penhoon UI
is Linux-only and expects systemd, `apt` and `iptables`; there is no Windows, Docker or
other-distro support.

**Architectures:** `amd64` · `arm64` (aarch64) · `armv7` · `s390x` — see the
[releases page](https://github.com/Arman2122/p-ui/releases) for the published assets.

## Database

Penhoon UI stores everything in **PostgreSQL**. It is the only supported backend.

The installer can install PostgreSQL from your distribution's packages and create a
dedicated role and database, or take a DSN for a server you already run. Either way it
writes the connection string to the service environment file, `/etc/default/p-ui`:

```
PUI_DB_DSN=postgres://pui:password@127.0.0.1:5432/pui?sslmode=disable
```

`PUI_DB_DSN` is **required**. If it is missing or unparseable the panel stops at startup
with an error naming the variable and showing an example DSN — it never falls back to
another store.

Backup and restore go through `pg_dump` / `pg_restore` from the `postgresql-client`
package: download a `.dump` from the panel's overview page (or let the Telegram bot send
one), and upload it back to restore. If a dump was produced by a newer PostgreSQL than the
server's client tools can read, `p-ui pgclient <major>` installs a matching client.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `PUI_DB_DSN` | PostgreSQL connection string — **required**, the panel will not start without it | — |
| `PUI_DB_MAX_OPEN_CONNS` | Maximum open connections (PostgreSQL pool) | — |
| `PUI_DB_MAX_IDLE_CONNS` | Maximum idle connections (PostgreSQL pool) | — |
| `PUI_INIT_WEB_BASE_PATH` | Initial URI path for the web panel | `/` |
| `PUI_ENABLE_FAIL2BAN` | Enable Fail2ban-based IP-limit enforcement | `true` |
| `PUI_LOG_LEVEL` | Log verbosity (`debug`, `info`, `warning`, `error`) | `info` |
| `PUI_DEBUG` | Enable debug mode | `false` |
| `PUI_TUNNEL_HEALTH_MONITOR` | Enable the tunnel health monitor (probes a URL and restarts xray after repeated failures; a restart drops all clients) | `false` |
| `PUI_TUNNEL_HEALTH_PROXY` | Proxy the probe is sent through; point it at a local xray inbound so the probe tests the tunnel (e.g. `socks5://127.0.0.1:1080`). Empty means the probe only checks host connectivity | — |
| `PUI_TUNNEL_HEALTH_URL` | URL probed for tunnel health | `https://www.cloudflare.com/cdn-cgi/trace` |
| `PUI_TUNNEL_HEALTH_INTERVAL` | Interval between probes | `30s` |
| `PUI_TUNNEL_HEALTH_TIMEOUT` | Per-probe timeout | `10s` |
| `PUI_TUNNEL_HEALTH_FAILURES` | Consecutive failures before a restart is triggered | `3` |
| `PUI_TUNNEL_HEALTH_COOLDOWN` | Minimum delay between consecutive restarts | `5m` |

## Languages

The panel UI ships in **English** and **فارسی (Persian)**.

## Contributing

Contributions are welcome. Please read the [Contributing Guide](/CONTRIBUTING.md) before
opening an issue or pull request.

## Acknowledgements

- [3x-ui](https://github.com/MHSanaei/3x-ui) by [MHSanaei](https://github.com/MHSanaei)
  (**GPL-3.0**) — the upstream panel Penhoon UI is forked from. This project would not
  exist without it.
- [alireza0/x-ui](https://github.com/alireza0/x-ui) — upstream of 3x-ui itself.
- [Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) (**GPL-3.0**) —
  routing rules with built-in Iranian domains, security and adblocking.
- [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat)
  (**GPL-3.0**) — routing rules based on domains and addresses blocked in Russia.

## License

Released under the [GPL-3.0](/LICENSE) licence, the same licence as upstream 3x-ui.
