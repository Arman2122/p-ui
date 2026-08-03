# Cloud deployment (unattended install)

Tooling to ship the Penhoon UI panel via unattended install, with **per-instance
credentials generated on first boot** (never `admin/admin`, never a shared
session secret).

**Target systems:** Ubuntu 22.04 / 24.04 / 26.04 or Debian 12+ on amd64 or arm64
(apt, systemd, iptables). Penhoon UI is Linux-only.

| Path | What it is | Use when |
| --- | --- | --- |
| [`cloud-init/`](cloud-init/) | Generic cloud-init user-data (unattended `install.sh`) | Any cloud, no image build |
| [`marketplace/hetzner/`](marketplace/hetzner/) | Hetzner Cloud notes | Hetzner deployments |
| [`test/`](test/) | Install smoke test (throwaway VM / CI runner) | Verifying the install path |

## How it works

`install.sh` runs unattended when `PUI_NONINTERACTIVE=1` or stdin is not a TTY.
Each instance installs and configures itself with random credentials. See
[`cloud-init/README.md`](cloud-init/README.md).

## Unattended install knobs

`install.sh` reads these env vars in non-interactive mode (all optional; unset ⇒
secure random / default):

`PUI_USERNAME`, `PUI_PASSWORD`, `PUI_PANEL_PORT`, `PUI_WEB_BASE_PATH`,
`PUI_SSL_MODE` (`none`|`ip`|`domain`, default `none`), `PUI_DOMAIN`,
`PUI_ACME_EMAIL`, `PUI_ACME_HTTP_PORT` (ACME HTTP-01 listener port, default `80`),
`PUI_SSL_IPV6` (optional IPv6 address to add to an `ip`-mode cert),
`PUI_SERVER_IP` (fallback IP for the displayed access URL when auto-detection fails),
`PUI_DB_DSN` (PostgreSQL connection string).

The resulting credentials are written to `/etc/p-ui/install-result.env` (mode 600).

## Database

PostgreSQL is the panel's only backend. `PUI_DB_DSN` is **required at runtime** —
the panel fails fast at startup when it is missing or unusable.

- Leave `PUI_DB_DSN` unset and `install.sh` apt-installs PostgreSQL on the box,
  creates a dedicated role + database, and writes the generated DSN.
- Set `PUI_DB_DSN=postgres://user:pass@host:5432/dbname?sslmode=disable` to use an
  existing server instead.

Either way the DSN lands in `/etc/default/p-ui` (mode 600), which the `p-ui`
systemd unit loads via `EnvironmentFile`. That file is the place for every other
`PUI_*` knob too: re-running the installer (and `p-ui`'s PostgreSQL menu)
rewrites only the `PUI_DB_DSN` line and leaves the rest alone, and `p-ui update`
never writes to it — so optional pool tuning can go straight in:

```bash
# /etc/default/p-ui
PUI_DB_MAX_OPEN_CONNS=50
PUI_DB_MAX_IDLE_CONNS=50
```

Restart after editing: `sudo systemctl restart p-ui`.

## Smoke test

```bash
sudo PUI_SMOKE_ALLOW_HOST=1 bash deploy/test/smoke-noninteractive.sh [version]
```

It installs the panel and a local PostgreSQL onto the machine it runs on, so use
a throwaway Ubuntu/Debian VM or an ephemeral CI runner — hence the explicit
`PUI_SMOKE_ALLOW_HOST=1` opt-in outside CI.
