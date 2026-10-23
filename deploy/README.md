# Penhoon installation roles

The role boundary is implemented, but master/combined startup remains deliberately blocked until the owned business runtime and mixed-job factory are connected. The installer fixtures below qualify filesystem transactions and role isolation with mocked processes; they do not claim a successful installed master/combined systemd deployment.

`PUI_ROLE` accepts `master`, `node`, or `combined`. Unknown values fail before startup. The temporary unset value retains the existing ordinary panel until the business cutover is complete. `p-ui role` describes the process role without opening PostgreSQL; `p-ui preflight` validates panel-role environment requirements without connecting to the database or changing the host. A node role cannot run the panel, database commands, or UI.

Master and combined run the same unprivileged management process in `penhoon-master.service`. It runs panel/subscription APIs, fleet control, database maintenance and notifications. Its core execution and mixed jobs require explicit fleet-owned handlers. Local core construction, supervision, traffic polling, host firewall/egress/shaping, Xray restart signals, tunnel monitoring, WARP refresh, core logs and host tuning are excluded. Combined adds the ordinary signed headless agent/runtime installation alongside this process.

## Inputs and fresh installations

Use Debian 12+ or Ubuntu 22.04, 24.04, or 26.04 with systemd. The master requires Python 3, OpenSSL, PostgreSQL client tools, CA certificates, and an operator-provisioned PostgreSQL database. It does not install VPN packages, DKMS, firewall rules, or sysctls. Control listeners use unprivileged ports (1024 or above); advertised endpoints may use a separately configured reverse proxy.

The installer and all adjacent templates must arrive through an authenticated channel. Supply the panel binary with its independently authenticated SHA-256 and the DSN in a root-owned, single-link `0600` file. Do not pass database credentials on the command line.

```sh
sudo python3 deploy/install.py --role master \
  --panel-binary /trusted/p-ui --panel-sha256 AUTHENTICATED_SHA256 \
  --database-dsn-file /private/postgresql-dsn \
  --control-address control.example.com:7443 \
  --enrollment-url https://control.example.com:7444/enroll \
  --server-name control.example.com
```

For combined mode, select `--role combined` and add the signed node bundle, publisher key, independently authenticated TUF root, and repository endpoints from [the node bootstrap instructions](node/README.md). The installer prepares local enrollment itself; do not supply an unrelated enrollment package. Native remains an explicit `--native` selection, subject to the separate native qualification gates.

```sh
sudo python3 deploy/install.py --role combined \
  --panel-binary /trusted/p-ui --panel-sha256 AUTHENTICATED_SHA256 \
  --database-dsn-file /private/postgresql-dsn \
  --control-address control.example.com:7443 \
  --enrollment-url https://control.example.com:7444/enroll \
  --server-name control.example.com \
  --release-dir /trusted/node-release --trusted-key /trusted/publisher.pem \
  --tuf-root /trusted/root.json \
  --metadata-url https://updates.example.com/metadata \
  --targets-url https://updates.example.com/targets
```

For node-only mode, select `--role node` with the signed node inputs and `--enrollment /private/enrollment.json`. It installs no panel binary or database environment. `--dry-run` validates supplied bootstrap artifacts without installing services; it does not prove TUF network freshness, database connectivity, or startup readiness.

The installer refuses existing ordinary panels, role conversions, and unowned node/master state. Retry the exact same inputs after an interrupted installation. Changed releases use their release update workflow; the ordinary `install.sh` and `update.sh` refuse role installations. The role menu (`penhoon`, or existing `p-ui.sh`) exposes status, logs, start/stop/restart, preflight, and panel settings. It offers no cross-role uninstall or identity reset.

## Combined enrollment and recovery

A root-owned `0700` bootstrap directory contains a `0600` enrollment request before any enrollment database side effect. Its request, node and installation UUIDs are derived once from the installation transaction; ticket and expiry are retained. `p-ui fleet enroll --request-stdin` accepts the persisted request through stdin, runs as the master service user, and returns the private package to the installer. Replaying the request retains the exact identity and ticket; conflicting or expired requests fail explicitly.

The package uses loopback control/enrollment addresses with the advertised TLS `ServerName` and pinned CA. There is no in-process or plaintext shortcut. The normal node vault, protocol generation 2, state generation 2, journals, agent unit and runtime units are used. Bootstrap files are deleted only after enrollment and node installation are confirmed. Interrupted runtime activation reuses the existing vault and request; it does not mint another node.

`localFleetNodeID` in the role record and `PUI_LOCAL_FLEET_NODE_ID` in the master environment are placement metadata only. They grant no local execution privileges. Normal UUID-based fleet enrollment and current-installation validation still apply.

## Qualification

Run the filesystem/process-fixture tests only in a private mount namespace:

```sh
sudo unshare --mount --propagation private bash tools/nodekernel/install-role-test.sh
sudo python3 tools/nodekernel/install-role-negative.py
```

The fixture masks `/etc`, `/var/lib`, `/var/log`, `/usr/local` and `/run`, then mocks accounts, service commands and node installation. Five tests cover master/node separation, ordinary/role conversion refusal, protected inputs, lost enrollment responses, failed activation, single enrollment, retained journals, and loopback TLS identity. Four negative controls remove role, retry, permission and TLS-name safeguards and require test failures. No host service/account changes are made.

Actual master/combined installation must subsequently run under the existing [isolated systemd namespace harness](../tools/nodekernel/namespace/README.md) using the real completed business factory, real master binary, PostgreSQL, and signed node release. This gate has not yet run for these roles.
