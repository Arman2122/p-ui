[English](/README.md) | [فارسی](/README.fa_IR.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/p-ui-dark.png">
    <img alt="Penhoon UI" src="./media/p-ui-light.png" width="320">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/Arman2122/p-ui/releases"><img src="https://img.shields.io/github/v/release/Arman2122/p-ui?label=release" alt="Release"></a>
  <a href="https://github.com/Arman2122/p-ui/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Arman2122/p-ui/ci.yml?branch=main&label=CI" alt="CI"></a>
  <a href="https://github.com/Arman2122/p-ui/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/Arman2122/p-ui" alt="Go"></a>
  <a href="/LICENSE"><img src="https://img.shields.io/badge/license-GPL--3.0-blue" alt="License"></a>
</p>

<p align="center">
  <strong>One panel for your proxy and VPN servers.</strong><br>
  Xray-core, kernel WireGuard, AmneziaWG and MTProto — clients, quotas, subscriptions and routing in one place.
</p>

<p align="center">
  <a href="https://arman2122.github.io/p-ui/en">Documentation</a>
  ·
  <a href="https://arman2122.github.io/p-ui/en/docs/guide/installation">Install guide</a>
  ·
  <a href="https://arman2122.github.io/p-ui/en/docs/reference/api">API reference</a>
  ·
  <a href="https://github.com/Arman2122/p-ui/releases">Releases</a>
  ·
  <a href="/CHANGELOG.md">Changelog</a>
</p>

---

## Contents

- [What it is](#what-it-is)
- [Install](#install)
- [Manage the panel](#manage-the-panel)
- [Update and uninstall](#update-and-uninstall)
- [Features](#features)
- [Configuration](#configuration)
- [Database](#database)
- [Supported platforms](#supported-platforms)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [Acknowledgements](#acknowledgements)
- [License](#license)

## What it is

**Penhoon UI** (`p-ui`) is a self-hosted web panel for running proxy and VPN servers
on Linux. It supervises [Xray-core](https://github.com/XTLS/Xray-core) and, beside it,
kernel **WireGuard**, **AmneziaWG** and **MTProto** — so a VLESS inbound, a WireGuard
tunnel and a Telegram proxy are managed from the same pages, with the same clients,
quotas, expiry dates and subscription links.

Everything is stored in **PostgreSQL**, the service runs under **systemd**, and several
servers can be joined into one deployment: a master panel with sub-nodes. The interface
is available in English and Persian, in dark and light themes.

> [!NOTE]
> Run it on servers you own or administer, and comply with the laws that apply to you
> and to your users.

## Install

### Requirements

| | |
| --- | --- |
| **Operating system** | Ubuntu 22.04, 24.04 or 26.04, or Debian 12 and newer. Other Debian-family distributions install with a warning. |
| **Access** | `root` on a server with a public IP address. `systemd` and `apt` are expected. |
| **Database** | PostgreSQL. The installer sets one up for you, or you point it at a server you already run. |

### One command

Run this as `root`:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)
```

The installer:

1. installs the service and its dependencies (including `nftables`);
2. installs PostgreSQL and creates a dedicated role and database, or asks for the DSN of
   an existing server;
3. generates a random **username**, **password** and **web base path**;
4. prints them, together with the **Access URL**, when it finishes.

Open the Access URL in a browser and sign in. Later, `p-ui settings` shows the port and
base path; lost credentials are replaced from the `p-ui` menu (*Reset Username &
Password*).

### Install a specific version

Append the release tag:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh) v1.5.2
```

### Unattended install

For cloud-init and other automation, set `PUI_NONINTERACTIVE=1` (or run the script with
no TTY). It installs end to end with no prompts and writes the generated credentials to
`/etc/p-ui/install-result.env`:

```bash
PUI_NONINTERACTIVE=1 bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)
```

Ready-made [cloud-init user-data](deploy/cloud-init/) and
[Hetzner Cloud notes](deploy/marketplace/hetzner/) are in [`deploy/`](deploy/).

### After the first login

The [first-login guide](https://arman2122.github.io/p-ui/en/docs/guide/first-login)
walks through the panel settings, SSL and the first inbound and client. Ports and
firewall rules are covered in the
[ports reference](https://arman2122.github.io/p-ui/en/docs/reference/ports-firewall).

## Manage the panel

`p-ui` opens an interactive menu: install, update, credentials, SSL certificates,
firewall, PostgreSQL, logs, BBR and more. Each entry is also a subcommand:

| Command | What it does |
| --- | --- |
| `p-ui start` / `stop` / `restart` | Control the service |
| `p-ui restart-xray` | Restart only Xray-core |
| `p-ui status` | Service status |
| `p-ui settings` | Show the port, base path and SSL state |
| `p-ui enable` / `disable` | Start on boot, or not |
| `p-ui log` | Tail the panel log |
| `p-ui banlog` | Device-cap bans and their log |
| `p-ui ban` | List device-cap bans; `-add <ip> [-minutes N]`, `-release <ip> [-email <client>]`, `-release-all` |
| `p-ui update` | Update to the latest stable release |
| `p-ui update-dev` | Update to the rolling `dev-latest` build |
| `p-ui legacy` | Install a specific older version |
| `p-ui update-all-geofiles` | Refresh the geo files and restart if they changed |
| `p-ui pgclient <major>` | Install a matching `postgresql-client` |
| `p-ui uninstall` | Remove the panel |

## Update and uninstall

- **`p-ui update`** downloads the latest release, verifies the archive against its
  published checksum, and restarts the service. The database and settings are kept.
- **`p-ui update-dev`** moves to `dev-latest`, a per-commit pre-release — useful for
  trying a fix before it ships, not for a server you rely on.
- **`p-ui legacy`** installs an older release by tag.
- **`p-ui uninstall`** removes the service and its files.

## Features

### Cores and protocols

- **Xray-core** — VLESS, VMess, Trojan, Shadowsocks, Hysteria, HTTP, SOCKS/mixed,
  WireGuard, Dokodemo-door (tunnel) and TUN inbounds. TCP, mKCP, WebSocket, gRPC,
  HTTPUpgrade and XHTTP transports, secured with TLS, XTLS-Vision or REALITY. Fallbacks
  serve several protocols on one port.
- **Kernel WireGuard and AmneziaWG** — native in-kernel tunnels. The AmneziaWG module is
  built and registered with DKMS by the installer, and per-client speed plans are
  enforced by the kernel.
- **MTProto** — Telegram proxies with per-client FakeTLS secrets, ad tags, quotas and
  expiry, served by the bundled `mtg-proxy`.

### Clients and subscriptions

- Traffic quotas, expiry dates, IP limits enforced on the panel's own firewall table, live online status,
  and per-client, per-inbound and per-outbound statistics with reset controls.
- One client across many inbounds, with share links, QR codes and a built-in
  subscription server that answers in raw, JSON and Clash formats, with
  [custom page templates](docs/custom-subscription-templates.md).

### Networking

- Outbounds and routing: routing rules, balancers, outbound chaining, WARP, and
  WireGuard or AmneziaWG exit tunnels that carry selected inbounds out through another
  server.
- DNS control, and policy-routed egress for the kernel tunnels.

### Operations

- **Multi-node** — one master panel manages sub-nodes over mTLS.
- **Telegram bot**, **e-mail alerts** and **LDAP** user sync.
- **REST API** with an OpenAPI document served at `/panel/api/openapi.json`, plus a
  WebSocket feed for live status.
- **Backups** through `pg_dump` / `pg_restore`, from the panel or the bot.
- An optional **tunnel health monitor** that restarts Xray after repeated probe
  failures.

## Configuration

| Path | Holds |
| --- | --- |
| `/usr/local/p-ui/` | The panel, the `p-ui.sh` script and `bin/` with Xray-core and the geo files |
| `/etc/default/p-ui` | The service environment file — `PUI_DB_DSN` and any other variable below |
| `/etc/p-ui/install-result.env` | Credentials written by an unattended install |
| `/var/log/p-ui/` | Logs |

The panel reads its options from the environment. Put them in `/etc/default/p-ui` and
restart the service.

| Variable | Default | Description |
| --- | --- | --- |
| `PUI_DB_DSN` | — | **Required.** PostgreSQL connection string, e.g. `postgres://pui:password@127.0.0.1:5432/pui?sslmode=disable`. The panel refuses to start without it. |
| `PUI_PORT` | — | Override the panel port; takes precedence over the stored setting |
| `PUI_INIT_WEB_BASE_PATH` | `/` | Web base path on the first launch |
| `PUI_IP_LIMIT_BANS` | `true` | Drop a device over its IP limit on the panel's own nftables ban set, on its inbound's port (`PUI_ENABLE_FAIL2BAN` is still read as the old name) |
| `PUI_SKIP_HSTS` | `false` | Skip the HSTS header when TLS terminates at a reverse proxy |
| `PUI_LOG_LEVEL` | `info` | `debug`, `info`, `notice`, `warning` or `error` |
| `PUI_DB_MAX_OPEN_CONNS` / `PUI_DB_MAX_IDLE_CONNS` | `25` | PostgreSQL connection-pool limits |

The full list — log folders, memory tuning, the tunnel health monitor and the
installer's own variables — is in the
[environment reference](https://arman2122.github.io/p-ui/en/docs/reference/env-vars).

## Database

PostgreSQL is the only storage backend. The installer either installs it from your
distribution's packages and creates a dedicated role and database, or takes the DSN of a
server you already run. Either way the connection string ends up in `/etc/default/p-ui`.

Backup and restore use `pg_dump` / `pg_restore`: download a `.dump` from the panel's
overview page (or let the Telegram bot send one) and upload it back to restore. If a
dump comes from a newer PostgreSQL than your client tools can read,
`p-ui pgclient <major>` installs a matching client.

## Supported platforms

- **Operating systems:** Ubuntu 22.04, 24.04 and 26.04; Debian 12 and newer. Linux only
  — systemd, `apt`, `iptables` and `nftables` are expected. There is no Docker image and
  no Windows build.
- **Architectures:** `amd64`, `arm64`, `armv7`, `armv6`, `armv5`, `386` and `s390x`.
  Every release ships one archive per architecture with a `.sha256` beside it.

## Documentation

The [documentation site](https://arman2122.github.io/p-ui/en) covers:

- **Guide** — [installation](https://arman2122.github.io/p-ui/en/docs/guide/installation),
  [first login](https://arman2122.github.io/p-ui/en/docs/guide/first-login),
  [update and uninstall](https://arman2122.github.io/p-ui/en/docs/guide/update-uninstall).
- **Configuration** — panel settings, inbounds, transports, REALITY, clients, share
  links, subscriptions and SSL certificates.
- **Operations** — backup and restore, multi-node, outbounds and routing, reverse proxy,
  security and the Telegram bot.
- **Reference** — database, environment variables, ports and firewall, and the
  [API reference](https://arman2122.github.io/p-ui/en/docs/reference/api), generated
  from the same OpenAPI document the panel serves.
- **Help** — FAQ, migration and troubleshooting.

## Contributing

Issues and pull requests are welcome. [CONTRIBUTING.md](/CONTRIBUTING.md) covers the
setup, the testing philosophy and the local gate (`make verify`), and
[docs/architecture.md](/docs/architecture.md) is the map of the codebase.

## Acknowledgements

- Penhoon UI began as a fork of [3x-ui](https://github.com/MHSanaei/3x-ui) by MHSanaei
  and keeps its GPL-3.0 licence. It has since been rebuilt around multiple cores,
  PostgreSQL and a new frontend.
- [Xray-core](https://github.com/XTLS/Xray-core) — the proxy engine.
- [WireGuard](https://www.wireguard.com/) and
  [AmneziaWG](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module) — the
  kernel tunnels.
- [mtg](https://github.com/9seconds/mtg) — the MTProto proxy that
  [`mtg-proxy`](https://github.com/Arman2122/mtg-proxy) builds on.
- [Iran v2ray rules](https://github.com/chocolate4u/Iran-v2ray-rules) and
  [Russia v2ray rules](https://github.com/runetfreedom/russia-v2ray-rules-dat)
  (GPL-3.0) — routing rule sets.

## License

[GPL-3.0](/LICENSE).
