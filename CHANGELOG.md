# Changelog

Operator-visible changes only — things that behave differently on a panel you
upgrade in place. Bug fixes that simply make a broken thing work are not listed
here; the git log is the full record.

## Unreleased

### Behaviour changes

- **Quota and expiry depletion now really restarts the core.**
  `restartXrayOnClientDisable` has always defaulted to on and always fired, but
  it asked for a *reconcile*, which pushes the user removal through the core API
  without replacing the process. The core does not close a connection that is
  already established when a user is removed — verified against Xray 26.7.28 —
  so a depleted client kept its tunnel, and with mux kept opening new requests
  over it indefinitely. The restart is now forced, which is the only lever the
  core exposes.
  **What you will notice:** when any client is disabled by quota or expiry, every
  connection on the box is briefly dropped while the core restarts. That was
  always what this setting promised; it was not delivering it. Turn
  `restartXrayOnClientDisable` off if you would rather revocation applied only to
  new connections.

- **A quota reset now actually restores access.** A client is disabled in three
  places and the core reads all of them; the periodic reset, the Clients page
  reset-all button, the Telegram bot's reset and an inbound's own reset each
  restored one. The panel showed a fresh allowance and the client still could not
  connect — on any protocol, and an Xray restart did not help, because the
  restart rebuilt from the same three places. mtproto clients also stayed blocked
  on the sidecar's own counter, which no reset cleared.
  **What you will notice:** clients stuck disabled after an earlier reset come
  back at their next one. A client the operator disabled by hand stays disabled,
  unless they were also over quota — in which case a reset does bring them back,
  which is what the per-client reset has always done. An inbound comes back from
  its own reset only if its own quota was what disabled it, and only if it is not
  also past its own expiry.

- **Per-client IP limits that survived an upgrade start being enforced once
  fail2ban is available.** They were always meant to be, and the panel has
  always ignored them while fail2ban was missing — but a limit set long ago,
  under a capability that has been dead since, becomes live again the moment
  the host can enforce it. Worth a look at the Clients page if that describes
  your panel.

- **An inbound whose TLS certificate file does not exist yet still saves.**
  Saving an inbound that points at a path certbot has not written is the ordinary
  order of operations and is no longer refused. The certificate's *shape* is still
  checked — an empty one is still rejected — and the core is never swapped for a
  config it cannot load, so a file that never appears cannot take the panel down.

### Security

- An upgrade no longer wipes configured per-client IP limits. The boot-time
  migration that zeroed them whenever fail2ban could not enforce is retired: it
  destroyed stored configuration to reflect a host capability the panel already
  handles at read time, and it fired on every upgrade because the panel starts
  before setup_fail2ban runs. Affects existing panels that had limits set and
  reached an upgrade with no working fail2ban, or with PUI_ENABLE_FAIL2BAN set
  to anything but true. Limits already wiped by an earlier upgrade cannot be
  recovered.

- A Telegram user can no longer read another client's subscription URL, config
  URIs or QR codes by sending a crafted callback naming their email. Client link
  callbacks now resolve ownership against the caller's Telegram id.

- Sessions now expire when the panel says they do. sessionMaxAge only ever
  reached the browser as a cookie attribute; the server kept accepting a copied
  cookie for 30 days regardless. Logging out also revokes every outstanding
  session for that admin, on every device, instead of only clearing the browser
  in front of you.
  **What you will notice:** on the upgrade, anyone signed in for longer than
  sessionMaxAge is signed out once, and a change to sessionMaxAge takes effect
  on the next panel restart.

- A fresh install no longer leaves `/usr/local/p-ui` owned by the release
  archive's build uid. Existing installs are repaired by the next run of the
  install or update script.
