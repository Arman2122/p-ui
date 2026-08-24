# Changelog

Operator-visible changes only — things that behave differently on a panel you
upgrade in place. Bug fixes that simply make a broken thing work are not listed
here; the git log is the full record.

## Unreleased

### Behaviour changes

- **A routing rule with nowhere to send traffic no longer takes Xray down, and
  a Block rule still blocks.** `direct` and `block` are not tags: they resolve
  to whichever outbound in your Xray template carries the `freedom` or
  `blackhole` protocol. A template with neither — a "nothing goes out directly"
  setup, or one with the `blocked` outbound removed — compiled those rules to an
  empty outbound tag, which xray-core rejects outright, so the core refused to
  start and every client on the box went offline over one rule. The two cases
  are now answered differently, both fail-closed: a Block rule gets a blackhole
  supplied for it, because a blackhole has no egress and cannot leak anything;
  a Direct rule is still refused, because a template with no freedom outbound is
  plausibly the policy "nothing leaves this box directly" and inventing one would
  punch through it.
  **What you will notice:** the core starts, and rules you marked Block keep
  blocking whatever your template looks like. The private-address guard the
  panel puts in front of each WireGuard-family inbound is also no longer skipped
  when the template has no blackhole. A Direct rule against a template with no
  freedom outbound does nothing until you add one.

- **Turning automatic banning off no longer turns the IP log off with it.** With
  `PUI_ENABLE_FAIL2BAN` set to false, or on a host where fail2ban was never
  installed, the panel stopped asking the cores who was connected at all — so
  every client's observed-address list, the `clientIps` endpoint behind it and
  both info modals stayed permanently empty, and nothing recorded who had
  exceeded their IP cap. Observing and deciding now always run; the toggle gates
  only the two halves that act — the line fail2ban's jail reads, and the
  disconnect.
  **What you will notice:** on a panel with banning off, addresses appear under
  each client again, and for a client past its cap the list includes the
  over-cap addresses too, because nothing is cutting them. Nothing new is
  banned: no line is written to the file the jail watches, so a jail left
  installed after the toggle was turned off stays quiet.

- **A client cut off for exceeding its IP limit is put back even if the core is
  briefly busy.** The cut is remove-then-add, and a single failure on the way
  back in used to leave the client removed from the running config with nothing
  to restore them — they stayed off until an unrelated edit or a restart rebuilt
  it. The re-add is now retried, and the whole inbound re-applied if every
  attempt fails.
  **What you will notice:** if a client has been unreachable since hitting its
  IP limit, restarting the core brings them back, and it will not happen again.

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

- **Editing an Xray inbound no longer drops its live connections.** Saving the
  inbound form used to remove that inbound from the running core and add it back,
  whatever had changed — so renaming an inbound, or editing one client on it,
  disconnected everyone using it. The edit is now converged in place and the core
  decides what actually moved, which for a client change is a user diff and for a
  remark is nothing at all.
  **What you will notice:** nothing, except that the disconnections stop.
  Disabling an inbound still removes it from the core, which is the one edit that
  has to.

- **AmneziaWG clients get a config that carries the obfuscation, and the two
  subscription formats that cannot carry it now say nothing instead.** The .conf
  the Clients page hands out is rendered by the panel rather than assembled in
  the browser, so it finally includes the inbound's parameters. Clash and the
  JSON subscription have no field for them at all, so an obfuscated AmneziaWG
  inbound is left out of those two rather than shipped as plain WireGuard.
  **What you will notice:** clients who downloaded a WireGuard config from the
  Clients page for an obfuscated inbound could never connect and now can, after
  re-downloading. And a subscriber on Clash or the JSON format will see that
  inbound disappear from their profile — it was there before, and it never
  worked. A plain WireGuard inbound is unaffected in every format.

- **Clearing an AmneziaWG obfuscation parameter now reaches the device.** The
  panel could add a parameter but never take one away: an omitted value meant
  "leave it alone" all the way to the kernel, so a device kept padding the form
  no longer showed. Both populations of clients are affected by the correction,
  in opposite directions.
  **What you will notice:** clients holding the config the panel handed out
  *after* the change have been unable to handshake ever since, and start working
  on the next apply. Clients still holding a config from *before* the change have
  been working against the stale device, and that same apply breaks them until
  they re-fetch their config. If you cleared parameters on a live inbound at any
  point, expect the second group to need a new .conf.

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
