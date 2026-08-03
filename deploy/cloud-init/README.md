# Penhoon UI via cloud-init

A single [`cloud-init.yaml`](cloud-init.yaml) user-data file that installs
Penhoon UI non-interactively on a fresh Ubuntu/Debian VM and generates **unique
random credentials per instance**. It works on any cloud-init platform.

**Supported images:** Ubuntu 22.04 / 24.04 / 26.04 or Debian 12+ (apt + systemd).
Penhoon UI is Linux-only; no other distribution is supported.

## How it works

1. The VM boots a stock Ubuntu/Debian cloud image.
2. cloud-init writes and runs `/opt/pui-bootstrap.sh`, which exports
   `PUI_NONINTERACTIVE=1` and pipes the project's `install.sh` into `bash`.
3. `install.sh` runs end-to-end with **zero prompts**, picking secure random
   values for any credential you didn't pin. Unless you set `PUI_DB_DSN`, it also
   apt-installs PostgreSQL, creates a dedicated role + database, and writes the
   generated DSN to `/etc/default/p-ui` (mode 600) — the env file the `p-ui`
   systemd unit reads.
4. The generated credentials are written to `/etc/p-ui/install-result.env`
   (mode 600) and echoed in full to the **serial console**. `/etc/motd` is
   world-readable, so it gets only the access URL and username.

Retrieve them after boot with either:

```bash
sudo cat /etc/p-ui/install-result.env     # panel credentials, over SSH
sudo cat /etc/default/p-ui                # PostgreSQL DSN
```

…or read the provider's **serial console** output (handy before you have SSH).

## Customising

Edit the `export PUI_*` lines inside the `write_files` block of
[`cloud-init.yaml`](cloud-init.yaml). All knobs are optional; unset ⇒ random/secure default.

| Env var | Default | Meaning |
| --- | --- | --- |
| `PUI_SSL_MODE` | `none` | `none` (plain HTTP), `ip` (Let's Encrypt IP cert), `domain` |
| `PUI_USERNAME` | random | Admin username |
| `PUI_PASSWORD` | random | Admin password |
| `PUI_PANEL_PORT` | random high port | Panel listen port |
| `PUI_WEB_BASE_PATH` | random | Panel base path (obscures the URL) |
| `PUI_DOMAIN` | — | Required when `PUI_SSL_MODE=domain` |
| `PUI_ACME_EMAIL` | — | Let's Encrypt account email (domain mode) |
| `PUI_DB_DSN` | local PostgreSQL | Point at an existing PostgreSQL server instead of installing one |

> **Database:** PostgreSQL is the panel's only backend and `PUI_DB_DSN` is
> **required at runtime** — the panel exits at startup if it is unset or
> unreachable. The installer guarantees it is set: either from the `PUI_DB_DSN`
> you provide (`postgres://user:pass@host:5432/dbname?sslmode=disable`) or from
> the local server it provisions. Don't remove `/etc/default/p-ui`.

> **TLS note:** `none` serves the panel over plain HTTP on a random high port —
> fine behind a reverse proxy or an SSH tunnel, but put TLS in front of it before
> exposing the panel publicly. `domain` mode needs a public DNS A record pointing
> at the box and port 80 reachable at install time.

## Per-provider usage

Pick an Ubuntu/Debian image on every provider below.

- **Hetzner Cloud** — *Create Server → Cloud config*: paste the file. Or CLI:
  `hcloud server create --image ubuntu-24.04 --user-data-from-file cloud-init.yaml ...`
- **AWS EC2** — *Advanced details → User data*: paste the file. Or
  `aws ec2 run-instances --user-data file://cloud-init.yaml ...`
- **DigitalOcean** — *Create Droplet → Advanced options → Add Initialization
  scripts (user data)*: paste the file. Or `doctl compute droplet create --user-data-file cloud-init.yaml ...`
- **Vultr** — *Deploy → Additional Features → Cloud-Init User-Data*: paste the file.
- **Google Cloud (GCE)** — `gcloud compute instances create p-ui \
  --image-family ubuntu-2404-lts-amd64 --image-project ubuntu-os-cloud \
  --metadata-from-file user-data=cloud-init.yaml`
- **Azure** — `az vm create --image Ubuntu2404 --custom-data cloud-init.yaml ...`
- **Oracle Cloud (OCI)** — *Create Instance → Show advanced options →
  Management → Cloud-init script*: paste (or base64-upload) the file. Choose the
  **Canonical Ubuntu** image, not Oracle Linux.

## Validate before you deploy

```bash
cloud-init schema --config-file deploy/cloud-init/cloud-init.yaml
```
