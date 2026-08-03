# Penhoon UI on Hetzner Cloud

Hetzner Cloud does **not** have a third-party image marketplace the way AWS does.
Ship Penhoon UI via **cloud-init**: each instance installs non-interactively and
generates unique per-instance credentials (no `admin/admin`, no shared secret).

## cloud-init (no image build)

Use the generic user-data from [`../../cloud-init/`](../../cloud-init/). It installs
P-UI non-interactively and generates unique per-instance credentials.

Pick an **Ubuntu 22.04/24.04/26.04 or Debian 12+** image (`ubuntu-24.04`,
`debian-12`, …) — Penhoon UI installs with apt + systemd and supports nothing
else. Both x86 (`cx*`) and Arm (`cax*`) server types work.

Web console: **Create Server → Cloud config** → paste
[`deploy/cloud-init/cloud-init.yaml`](../../cloud-init/cloud-init.yaml).

CLI:

```bash
hcloud server create \
  --name p-ui-1 \
  --type cx22 \
  --image ubuntu-24.04 \
  --user-data-from-file deploy/cloud-init/cloud-init.yaml
```

After boot, fetch the generated credentials:

```bash
ssh root@<server-ip> 'cat /etc/p-ui/install-result.env'
```

The panel stores its data in PostgreSQL. With no `PUI_DB_DSN` set, the install
provisions a local PostgreSQL server on the same instance and pins the generated
DSN in `/etc/default/p-ui`; point `PUI_DB_DSN` at a managed server if you'd
rather keep the database off the box.

## "App"-style listing

Hetzner's curated apps live in the community repo
[`github.com/hetznercloud/apps`](https://github.com/hetznercloud/apps): each app
is essentially a documented cloud-init config plus metadata. To propose Penhoon UI
as a Hetzner app, follow that repo's contribution pattern and base the app's
cloud-config on [`deploy/cloud-init/cloud-init.yaml`](../../cloud-init/cloud-init.yaml).
