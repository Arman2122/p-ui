# Changelog

Operator-visible changes only — things that behave differently on a panel you
upgrade in place. Bug fixes that simply make a broken thing work are not listed
here; the git log is the full record.

## Unreleased

### Behaviour changes

- **A mixed (SOCKS+HTTP) inbound no longer breaks its own UDP.** The panel
  filled in a "UDP IP" of `127.0.0.1` on every mixed inbound it created. Xray
  binds the SOCKS UDP relay to that address *and* hands it back to the client as
  where to send datagrams, so every remote user was told to send UDP to itself
  while the relay listened on the server's loopback. TCP worked, so the inbound
  looked healthy; DNS, QUIC and games over UDP were dead.
  **What you will notice:** new mixed inbounds leave the field empty, and Xray
  advertises the address the client actually connected to. **Inbounds you
  already created keep the stored `127.0.0.1` and are not changed for you** —
  the panel cannot tell its own default apart from a local-only proxy somebody
  set on purpose. If you have a mixed inbound with UDP switched on, open it,
  clear the UDP IP field and save.

- **The firewall menu no longer locks you out of your own server.** "Install
  Firewall" allowed ssh, http, https, 2053 and 2096 and then force-enabled ufw —
  but the installer assigns the panel port at random, so 2053 was almost never
  the real one and no inbound port was in the list at all. Menu option 5 was
  worse: a bare `ufw enable` with no rules, which default-denies everything the
  moment it runs. And `allow ssh` means port 22, so anyone who had moved sshd
  was locked out regardless.
  **What you will notice:** both entries now read the real ports from the panel
  — including inbounds you have switched off, so enabling one later is not eaten
  by a firewall nothing re-runs — print exactly what they will apply, and refuse
  to enable anything unless the port your own SSH session is on is in that list.
  A host that also serves another panel's inbounds still needs those added by
  hand; the menu says so before it asks.

- **Switching the Xray version works on 32-bit ARM.** The download built its
  filename from a list of architecture names Go never produces, so every armv5,
  armv6 and armv7 panel asked GitHub for a file that has never existed and got a
  404 — every time, on three platforms this project ships and documents. The
  correct name is now recorded when the panel is built, because it cannot be
  worked out afterwards and must not be guessed: the armv7 build of Xray crashes
  with an illegal instruction on an armv5 board.
  **What you will notice:** the version switcher works on those platforms. A
  panel built outside the release pipeline still refuses to download rather than
  installing a binary the board may not be able to run.

- **A mistyped panel port during install no longer leaves the box on default
  credentials.** The installer set the port, username, password and base path in
  a single command, and an unusable port aborted that command before the
  credentials were applied — so the panel stayed reachable on admin/admin while
  the installer printed the random credentials it had not set and wrote them to
  `install-result.env` as though the machine were secured. The port is checked
  first, and the credentials are applied by their own command whose result is
  checked.

- **A failed update no longer leaves the panel stopped with no service unit.**
  The updater stopped p-ui, disabled it, deleted its systemd unit and reloaded
  the daemon — and only then fetched its management script from GitHub. On a host
  that cannot reach `raw.githubusercontent.com`, which is many of the hosts this
  panel runs on, one failed request left the box with the panel down, no unit and
  no autostart, in a state that survives a reboot. That script now comes out of
  the release archive already downloaded, before anything is torn down, and a
  refresh that fails is a warning rather than the end of the update — the copy
  already installed still works.

- **An unattended update no longer reconfigures TLS on its own.** Updating
  without a terminal — from a script, a cron job, the panel's own update button —
  reached a prompt that reads from standard input, got end-of-file, and took that
  as agreement: it contended port 80 with whatever was already there, issued a
  six-day IP certificate and switched the panel to direct TLS. That prompt is now
  interactive-only, and the "SSL enabled" banner is printed only when a
  certificate was actually issued rather than unconditionally.

- **LDAP sync works on directories with more than about a thousand users, and
  auto-delete never acts before it has a trusted baseline.** Two changes that
  ship together because the second is what makes the first safe.
  The user search was unpaged, so a directory that limits how many entries one
  search may return answered "size limit exceeded" and the whole sync failed —
  no flag applied, no client created, no client deleted, ever, with one log line
  a minute as the only symptom. It is now paged. *(A directory that caps the
  total result set rather than the page size is a different case and is not
  helped by paging; there the fix is a directory-side limit for the panel's bind
  account.)*
  Auto-delete's safety guard also protected for exactly one run: a sync that came
  back with too few users was correctly refused, and then that same small number
  was recorded as the count the next sync would be measured against — which
  passed, and detached everyone. Only a sync the guard believed now updates that
  baseline. And because the baseline is held in memory, every panel start is a
  first sync: the first one after a start now records a baseline and deletes
  nothing, so auto-delete never acts against a number it has not seen twice.
  **What you will notice:** on a large directory, sync starts working. If you
  deliberately off-board most of your directory at once, auto-delete will refuse
  it as a suspicious collapse — restart the panel, let one sync record the new
  smaller size, and deletion resumes on the next run.

- **A VLESS client added with the `xtls-rprx-vision-udp443` flow can now
  connect.** That flow is a client-side setting: the client shortens it before it
  goes on the wire, and the server refuses any account whose stored flow is not
  what arrived. The panel already stripped it when it rebuilt the whole
  configuration, but not when it added a single client to the running core — so
  the client was accepted, reported as created, and could not connect, with no
  restart scheduled because nothing had failed. It stayed that way until some
  unrelated edit happened to rebuild the inbound.
  **What you will notice:** clients added with that flow work immediately. If you
  have one that has never connected, removing and re-adding it is enough.

- **Renaming or deleting an Xray outbound no longer leaves routing rules aimed
  at nothing.** A rule pointing at a deleted or renamed outbound kept matching
  traffic, and the core then dropped everything it matched — it does not fall
  back to another outbound, it closes the connection. So an inbound's customers
  went dark with the rule still shown as healthy and nothing explaining why.
  **What you will notice:** saving an Xray template that no longer carries an
  outbound or balancer a rule names disables that rule and says so in its remark,
  naming the destination that went away. Rules aimed at an uplink are untouched —
  an uplink is not a template tag — and so is a rule you had already disabled.

- **An uplink the panel cannot actually configure is no longer offered as a
  routing destination.** An uplink was saved without its own settings ever being
  read, so a typo in its address was accepted — and the routing compiler kept
  offering it, because the core reports a device name for an uplink without
  looking at its configuration. The rule then compiled to a marked outbound
  while the kernel rule that catches that mark was silently not installed, and a
  mark nothing matches falls through to the ordinary route: every client on that
  rule left with the server's own public address, with the rule shown as
  healthy.
  **What you will notice:** saving an uplink whose settings cannot be realised
  now fails with the driver's own reason. An uplink already stored in that state
  disappears from the destination picker and any rule naming it is refused until
  it is fixed — the traffic stops rather than leaving with the wrong identity.

- **A routing rule the compiler would drop is now refused instead of stored.**
  The compile ran on every config build and its diagnostics were thrown away, so
  a rule the planner marks inert saved cleanly, appeared in the list as active,
  and then was simply not emitted. For an exit rule that means every client of
  that inbound left with the server's own address instead of through the uplink,
  with nothing in the panel saying so.
  **What you will notice:** saving or editing a rule the planner cannot realise
  now fails with the reason — a criterion the inbound cannot match on, an exit
  that no longer resolves, an outbound your Xray template does not have. Nothing
  is written and nothing is provisioned. Existing stored rules are untouched;
  those that have gone inert since they were saved are now reported in the panel
  log on each config build.

- **A routing rule scoped to one inbound can no longer be silently widened to
  all of them.** The panel resolved a rule's inbound tags to ids before saving,
  and read "nothing resolved" as "the operator chose every inbound". Three
  ordinary situations produce exactly that, and each of them persisted the
  widened rule on the very next click:
  an inbound that has been **disabled** (it stops being a routing subject);
  an inbound that has been **deleted** (its rule is left naming nothing);
  and a page where the inbound list had **not finished loading** or its request
  failed, which is silent by design and leaves the list empty.
  A rule that blocked one customer began blocking every client on the box; a
  rule sending one customer through an uplink sent everyone.
  **What you will notice:** editing such a rule is now refused with a message
  naming the inbound the panel could not resolve, and toggling one on or off
  changes only that — it no longer recomputes the rule's scope, destination or
  matchers.
  **Worth a review:** the stored scope of a widened rule is indistinguishable
  from one an operator deliberately set to every inbound, so the panel cannot
  find them for you. Check the scope of any rule that named an inbound you have
  since disabled or deleted, and of any rule you toggled while the page was
  still loading. Re-enabling a rule whose inbound was deleted now fails with an
  error rather than appearing to work; that rule needs its inbound list set
  again.

- **Switching the Xray version no longer takes the proxy down when it fails.**
  The update stopped Xray first and downloaded afterwards, and every failure in
  between — a release asset missing for your architecture, a checksum mismatch, a
  dropped connection — returned without restarting anything, leaving the box with
  no proxy and the old binary still installed. It also broke the download it had
  just prepared for: the panel's own egress proxy is hosted *by* Xray, so on a
  filtered network the proxy an operator configured to reach GitHub was
  guaranteed unavailable exactly when it was needed.
  **What you will notice:** the download, checksum and extraction all happen with
  the current version still running and serving clients, and the switch is a
  single restart at the end. A failed update now leaves the panel exactly as it
  was, with an error. If the new version cannot load your configuration, the
  restart is refused and the running version keeps serving rather than the box
  going dark.

- **An inbound can no longer take the panel's own port.** The conflict check
  looked at other inbounds and at Xray's internal API port, but never at the two
  ports the panel process binds itself — the web panel (2053 by default) and the
  subscription server (2096, on every interface, enabled by default). Both are in
  Cloudflare's proxied-HTTPS set, which is exactly why an operator picks them for
  a CDN-frontable inbound. The panel accepted it, and Xray then failed to bind
  and exited: not that one inbound, the whole process, so every client on the box
  went offline and the watchdog restarted it into the same failure every couple
  of seconds while the panel showed the inbound as enabled.
  **What you will notice:** creating or editing such an inbound is refused, naming
  which listener holds the port. A UDP-only inbound may still share it, since both
  panel listeners are TCP. If you already have an inbound sitting on one of those
  ports, it must be moved to a free port before any other edit to it will save —
  it is already stopping Xray from starting. Turning the subscription server off
  releases its port. Where `PUI_PORT` is set, that is the port reserved, not the
  one stored in settings.

- **Upgrading now builds the AmneziaWG kernel module.** `install.sh` registered
  it through DKMS; `update.sh` never mentioned it. So a panel installed before
  AmneziaWG shipped and upgraded the ordinary way never got the module, however
  many times it updated — the protocol was simply absent from the UI, with no
  explanation, and the panel cannot build one for itself. Every AmneziaWG fix
  released since was invisible on those hosts for the same reason.
  **What you will notice:** the next update spends about a minute building the
  module, and AmneziaWG appears. A host without kernel headers or a toolchain
  still upgrades normally and keeps every other protocol; the build says so and
  carries on. The module is also rebuilt when an update ships changed module
  sources, which previously did not happen — DKMS keys on the kernel and on a
  version upstream never changes, so the old module stayed registered.

- **A failed geofile update no longer corrupts the geofile or bounces Xray.**
  The download opened the live `geoip.dat`/`geosite.dat` and truncated it to
  zero before reading a single byte of the response, with no temporary file, no
  verification and no rollback — and then restarted Xray whether or not the
  download had succeeded. A connection dropped partway through, over exactly the
  filtered links this panel is built for, left a corrupt geofile on disk and the
  core restarted onto it. The fetch also had no timeout of any kind.
  **What you will notice:** the file on disk is only ever replaced by a complete,
  structurally verified download, and Xray is not restarted when an update
  failed. A half-downloaded geofile is now refused with a message naming the
  file; before, it was installed, and whether the core noticed depended on which
  countries your routing rules happened to name.

- **AmneziaWG custom headers survive a save, and clearing one reaches the
  device.** h1..h4 and the rekey/keepalive timers are settable through the API
  but not rendered by the inbound form, and the form saved only the fields it
  knew — so the first innocuous edit in the panel, a rename or a port change,
  deleted them from the stored config. The device did not lose them at the same
  moment: an omitted value means "leave it alone" to the kernel, so it kept
  running the operator's custom headers while every config the panel handed out
  had none. Clearing a header was impossible for the same reason.
  **What you will notice:** two groups of clients move in opposite directions on
  the next apply, exactly as with the obfuscation-parameter fix. Clients holding
  a config generated *after* the headers were silently dropped have been unable
  to handshake ever since, and start working. Clients holding a config from
  *before* that have been working against the stale device, and stop until they
  re-fetch. If you set h1..h4 through the API and have since edited that inbound
  in the panel, expect the second group. A cleared header now sets the device
  back to WireGuard's own values rather than leaving the old ones in place.

- **A save the core refused is no longer reported as a success.** For AmneziaWG,
  WireGuard and MTProto the panel converges the device itself — the push *is* the
  apply, and there is no later Xray restart that repairs it. A refused apply was
  written to the debug log and nowhere else, so the panel said "Inbound has been
  successfully updated", the row went green, and the device kept running the
  previous configuration while every config the panel handed out described the
  new one.
  **What you will notice:** when the core refuses, the save still completes — the
  row really is saved — and a warning appears on the Inbounds page next to the
  success message, carrying the core's own reason and staying up for ten seconds.
  Xray-served inbounds are unchanged: a failed push there still just schedules the
  restart that fixes it, and says nothing.

- **One broken AmneziaWG inbound no longer strips the obfuscation from the
  others.** The convergence pass applied every AmneziaWG inbound's parameters
  only if none of them had failed first — so one inbound with an unreadable
  setting, a missing key or a device that would not come up meant every *other*
  AmneziaWG inbound on the box came up as plain kernel WireGuard. Each pass took
  the same exit, so it persisted for as long as the broken one did, and the panel
  showed all of them as converged.
  **What you will notice:** if you have more than one AmneziaWG inbound and one
  of them is unhealthy, the rest start carrying their obfuscation again on the
  next pass — which means their clients start connecting and any client that had
  been getting through on plain WireGuard stops. Fix or disable the broken
  inbound to see its own error.

- **An AmneziaWG inbound with header protection now refuses to save unless all
  four padding sizes are set.** The kernel requires every one of s1..s4 to be at
  least 12 bytes once a header-protection key is present, and treats an unset
  value as zero rather than as "leave it alone" — so the panel's own preset,
  which fills in s1 and s2 and leaves s3 and s4 empty, produced a device the
  module refused. It refused *after* applying the junk parameters and without
  logging anything, so the inbound came up carrying half its obfuscation and no
  header protection at all, while every client config the panel handed out
  claimed a key the server had never installed. Nobody could connect and the
  panel reported the save as successful.
  **What you will notice:** saving that combination now fails immediately and
  names the field to fill in. If you have an inbound in this state, set the two
  missing padding sizes (or clear the header-protection key) and save; the next
  apply repairs the device.

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

- **A client, node, subscription or routing rule created with its switch off is
  no longer stored switched on.** A zero-valued boolean was dropped from the
  INSERT whenever its column carried a database-side default, so the database
  supplied `true` instead. The consequences differed by row: a **client** staged
  as disabled was stored enabled, was therefore not stripped from the push to
  the core, and could connect; a **node** staged as off was heartbeated and had
  inbounds synced to it; an outbound **subscription** staged as off was fetched
  and its outbounds merged into the config; a **routing rule** staged as off
  started routing traffic on save.
  **What you will notice:** the switch is respected on create. Nothing else
  changes.
  **Worth checking by hand:** rows created this way before the fix are
  indistinguishable from rows that were deliberately enabled — there is no record
  of what was intended, so the panel cannot find them for you. If you ever added
  a client, node or subscription with its switch off and later found it active,
  that is this. The fix governs new creates only, for the same reason: there is
  nothing to repair from.

- A login attempt can no longer be used to flood the operator's inbox or
  Telegram admins. `login.attempt` was exempt from the notification cooldown on
  both subscribers, and `/login` needs no authentication, so anyone could loop
  requests against it and have the panel send one email and one Telegram message
  per round trip for as long as they cared to. Both are on by default. The
  Telegram half also risks the bot being rate-limited, which silently stops
  `xray.crash` and `node.down` reaching anyone. Login notifications are now
  held to the same one-per-minute cooldown as everything else, bucketed by
  outcome rather than by client address so that rotating the address does not
  reset it — and a real sign-in is still reported while failures are being
  throttled alongside it.

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
