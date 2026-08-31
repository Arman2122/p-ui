# Changelog

Operator-visible changes only — things that behave differently on a panel you
upgrade in place. Bug fixes that simply make a broken thing work are not listed
here; the git log is the full record.

## Unreleased

### Behaviour changes

- **Turning this host into a router no longer costs it its own IPv6.** The panel
  enables IPv6 forwarding for an L3 inbound, and the kernel's `accept_ra`
  default means "accept Router Advertisements *unless* forwarding is enabled" —
  so on a provider that hands out IPv6 by RA (AWS, Vultr, Linode; Hetzner and
  other statically-configured hosts are unaffected) the host lost its own IPv6
  default route, typically at the next reboot. The panel now raises `accept_ra`
  to 2 first, and only on an interface the kernel is actually configuring from
  an RA — never on a tunnel, where it would let a client advertise itself as
  this host's gateway. **Check a host with `ip -6 route show default`:**
  `proto ra` was affected, `proto static` was not. A host already in this state
  needs `accept_ra` set to 2 once by hand; after that the panel keeps it.
- **An AmneziaWG exit can be created.** The outbound picker offered `awgkernel`
  and the form then refused it — "this build has no form for that exit
  protocol" — because the panel had a form for kernel WireGuard exits only.
  It now has both, with the obfuscation fields the far end has to match.
- **The kernel WireGuard inbound form no longer tells you to set up NAT by
  hand.** The panel enables host forwarding, persists it across reboots and
  writes the NAT rule itself, at every save and every 10 seconds — but the form
  still printed five commands to run as root, ending in a second nftables
  table (`ip pui_nat`). **If you ran those commands, delete that table**:
  `nft delete table ip pui_nat`. It is not one the panel manages or removes,
  and its rule also translates traffic leaving on a routing front, which hides
  a client's real address from any routing rule that matches on source. The
  notice now appears only when the host is genuinely refusing, and says what
  the host reported.
- **A host with no `nft` says so instead of dropping every packet.** The NAT
  rule needs the `nft` binary; without it the panel still routed, so an L3
  inbound handed out addresses, completed handshakes and had everything it
  forwarded dropped upstream, with nothing on any screen. **Installing and
  upgrading now install `nftables`**, and a host that still lacks it is named
  in the inbound form.
- **A kernel WireGuard or AmneziaWG inbound is ready the moment you save it.**
  The host knobs it needs were converged on a 10-second timer, so an inbound
  created, given a client and handed out inside that window came up and carried
  nothing.
- **AmneziaWG is actually installed now.** The module was never built on any
  host: its build script shipped without the executable bit, and both the
  installer and the updater skipped the build whenever that bit was missing —
  so the panel correctly greyed AmneziaWG out everywhere as a core the host
  could not run. **Upgrading repairs it**, with no action from you.
  **An upgrade also refreshes the module source**, which it never did: a panel
  installed before AmneziaWG existed had nothing to build however often it
  updated, and one that had it kept rebuilding the previous release's copy.

- **AmneziaWG can build outside Debian and Ubuntu.** Its prerequisites were
  installed with `apt` only, so the module could never be built on Fedora,
  RHEL, Rocky, Alma, Arch or openSUSE. The build script now detects the host's
  package manager. Alpine is refused by name rather than attempted — musl ships
  no DKMS-capable kernel tree, so no amount of installing would help.
  **Note:** `install.sh` itself is still apt-only; this covers the kernel module.

- **`awg-dkms.sh status` reports a loaded module as loaded.** It said `no` for a
  module that was loaded and working: `grep -q` exits on its first match, `lsmod`
  takes SIGPIPE, and the script's own `pipefail` then failed the check. Display
  only — nothing decided anything on it — but it is the first thing an operator
  runs when the panel greys the protocol out, and it was lying to them.

- **A kernel upgrade will not silently take AmneziaWG away.** The install now
  checks that DKMS really registered the module against the running kernel —
  the thing that makes it rebuild itself after a kernel change — and says so if
  it did not, instead of the option quietly greying out after a reboot.


## v1.5.0 — 2026-08-31

### Behaviour changes

- **Bulk adjust can set an exact value, not only add to one.** "Make everyone
  exactly 50 GB" needed you to know each client's current quota and do the
  arithmetic per client; there is a mode toggle now.
  **Add stays the default and the first option**, so nothing an operator already
  does changes, and the confirm button says which mode is about to run and on
  how many clients.
  **Two things worth knowing about Set:** 0 leaves a field alone rather than
  making it unlimited — so filling in only a quota does not strip everyone's
  expiry — and a delayed-start client is skipped and reported, because writing
  an absolute date over a countdown that has not begun changes what that client
  was sold. Unlike Add, Set does limit a client that currently has no limit:
  that is the request.

- **A certificate the panel cannot serve is refused at install time, not
  discovered later.** `p-ui cert` stored two paths without ever opening them and
  always exited 0 — so handing the installer a key belonging to a different
  certificate, an expired file, or a path with a typo printed a green tick,
  raised the URL to https and restarted the panel. The panel then served plain
  HTTP on an https address you had just been told was ready, with the reason
  thrown away. The pair is now loaded and checked, the command reports failure,
  and the installer offers you the paths again (or a way out to no-SSL). Same
  fix in `update.sh`, which carried the identical case.

- **LDAP auto-delete no longer detaches clients you made by hand.** Its rule was
  "every client on this inbound the directory did not return" — which on an
  LDAP-managed inbound is every contractor, test identity and founder's own
  account. The first off-boarding sweep detached all of them from every
  configured inbound, and the log line read exactly like an intended removal.
  The panel now records which clients the sync created, and only those are
  swept; anything kept is named in the log.
  **What you will notice after upgrading:** every client that already exists is
  treated as hand-made and will never be auto-deleted, including ones LDAP
  really did create. Marking them by matching today's directory would be
  guessing, and guessing wrong deletes an account. Clients the sync creates
  from now on are swept normally.
  **Unchanged:** LDAP still disables an off-boarded user panel-wide. Disabling
  is one click to undo; detaching from every inbound is not.

- **Routing rules can match domains on kernel WireGuard and AmneziaWG.** "Send
  google.com through this exit" now works for an L3 inbound, not only for an
  Xray one — the panel makes the inbound read the name out of the connection
  when, and only when, a rule asks for one.
  **This also fixes a live gap on Xray's own inbounds:** sniffing defaults to off
  on a new inbound, so a domain rule on a VLESS or VMess inbound used to save,
  display as active, and never match a single connection. It matches now.
  **What you will notice:** the name is read from the TLS SNI, the HTTP Host
  header or QUIC, so traffic carrying no name still falls through to the IP
  rules; the connection's destination is unchanged, so IP rules and the
  private-address guard keep working; and your inbound's own sniffing setting is
  never rewritten — the change is in the generated config alone.

- **An MTProto inbound routed through Xray no longer picks a bridge port
  something else is using.** The internal loopback port was taken from whatever
  the OS had free for an instant, or from whatever an API caller sent, and never
  checked against the panel's own inbounds. On a collision the config generator
  skipped the bridge — so the inbound saved cleanly, displayed as routed, and
  carried no Telegram traffic at all, with only a log line to say why. The port
  is now checked and reallocated, including on later saves, since the collision
  can arrive after the inbound was created.

- **A broken Xray template is refused when you save it, not at the next
  restart.** The save built every outbound and nothing else, so a routing rule,
  a dns block or a policy section the core cannot load stored as a success — and
  the panel then refused every reconcile from then on, over a config it had told
  you was fine. The save now asks the core itself, the same load a start
  performs.
  **A template that is already rejected stays editable**, so it can be repaired
  through the panel rather than the database. As with every save-time refusal
  added by this campaign, only an edit that *introduces* the breakage is
  refused.

- **A tunnel inbound with no destination is refused instead of forwarding to
  itself.** With follow-redirect off and no rewrite address, xray substitutes
  127.0.0.1 and the connection's own port — so the inbound dials itself, and one
  external probe opens connections until the core running every other inbound on
  the box is out of file descriptors. The panel shipped that as the default
  tunnel configuration. The form now warns before you save, and the save refuses
  it.
  **An inbound already in that state stays editable** — you can still rename or
  disable it, and only an edit that *introduces* the loop is refused. That rule
  is how every save-time refusal added by this campaign behaves.

- **Clash subscriptions can shuffle their proxy order.** A new subscription
  setting randomises the order of the generated proxy group on every fetch, so
  clients stop all landing on whichever host sorts first — mihomo's own
  `shuffle-proxies-order`, and off by default. DIRECT stays last.
  **Replaces a host switch that never worked:** the per-host "Shuffle host"
  toggle was read by nothing and could not have been — order belongs to the
  list, not to one member of it. It and "Mihomo X25519" (whose output the
  panel already emits unconditionally) are now deprecated: still stored and
  still returned by the API, marked deprecated in the OpenAPI schema, and due
  for removal in a future release.

- **A TUN inbound no longer holds a port it never binds.** An Xray TUN device
  is a kernel interface, not a listener, but the panel demanded a port for it,
  counted that value as occupied, and refused a real inbound on it — naming a
  conflict with something that was not listening. The port field is now optional
  for a TUN inbound and says why, and the conflict check skips it.

- **A dead node no longer stalls traffic accounting.** When a depleted client
  sat on an inbound a remote node hosts, the push telling that node about it was
  made while holding the single writer every traffic poll and every client edit
  queues on — so a node that had gone away froze quota accounting and admin
  saves for the whole remote timeout. The push now happens after that writer is
  released.

- **A host's Host header now reaches a gRPC inbound.** gRPC has no host
  parameter — under HTTP/2 the `:authority` header is the Host header — and the
  override only rewrote parameters the transport already emitted, so a Host
  header set on a gRPC host row was stored, shown in the form, and dropped from
  every link it was meant to shape. The Host header and Path fields now say
  which transports they apply to; Path still has no gRPC equivalent.

- **The client form says which field it rejected.** A bad value raised a
  page-level toast naming the reason and not the input — across three tabs, of
  which one is on screen. The field is now marked and the reason sits under it.
  The numeric limits also had no translations, so a rejected quota, renew
  period, IP limit or Telegram id came back in Zod's own English.

- **Two admins can add clients through the Telegram bot at the same time.**
  The in-progress client was a set of package-level variables — one draft for
  the whole panel — so two admins running the wizard together wrote into it in
  turn, and whichever pressed Submit first saved a mix of both: the other's
  email, limits and inbound set, under their own name. Each chat now has its
  own draft. Two admins sharing one group chat still share one, because they
  already share the state machine that drives it.

- **Long admin operations are no longer cut off at 30 seconds.** Installing an
  Xray version, updating the geo files, scanning REALITY targets, downloading a
  database backup and sending one to Telegram all outrun the panel's 30-second
  write deadline. The connection died mid-response while the work ran on to
  completion — and the obvious reaction, retrying, started a second one. Those
  five routes now get the room their own timeouts already assume.

- **Quotas and expiry are enforced on a panel that does not run Xray.** The
  sweep that disables an over-quota or expired client ran inside the traffic
  poll, and that poll returned early when nothing had reported bytes and Xray
  was down. On a panel serving only kernel WireGuard or MTProto that was every
  poll: an expired client was never disabled, and its peer stayed connectable
  indefinitely.

- **Subscription paths and the JSON/Clash toggles apply when you save them.**
  Which routes the subscription server has was read once, at boot: enabling
  Clash subscriptions, or moving `subPath`, registered nothing until the panel
  was restarted — while the panel handed out the new URL the moment it was
  saved, so every link copied in between answered 404. The router is now
  rebuilt on save, and a rebuild that fails keeps the one that is serving.
  **Still needs the restart button:** the subscription port, listen address,
  certificate and the master subscription switch, which all rebind the
  listener.

- **A new WireGuard uplink is routed by the save that creates it.** Convergence
  read the host once, then created the device, then planned against that read —
  so the device it had just made was still absent, and the uplink's table got
  its blackhole and no route out. Everything attached to it was dropped until
  the 10-second drift job re-read the host, and the uplink's own rp_filter
  setting waited for the same pass. A probe run straight after the save read a
  working uplink as dead.

- **A depleted client on a userless inbound no longer restarts Xray.** The
  http, mixed, tunnel and tun kinds hold no users, so revoking a client on one
  answers "proxy is not a UserManager" -- which the quota job read as a failure
  a restart would repair. It restarted Xray, dropping every live connection on
  the box, and the next poll did it again.

- **A routing rule on an MTProto inbound only offers criteria that can match
  it.** The editor offered user, domain, protocol and attributes there, and none
  of them can: MTProto's traffic reaches the router over an internal bridge that
  authenticated nobody and inspects nothing. A rule using any of them saved,
  displayed as active, and matched no packet.

- **Clients show as online on a panel that does not run Xray.** The whole
  online view -- the client list's dot, the dashboard's online count, the
  subscription page, the Telegram bot's /onlines -- was stored inside the Xray
  process, so a panel serving only MTProto or kernel WireGuard reported every
  client offline forever, and a master with no local Xray lost its nodes' online
  lists too.

- **The client form stops offering an IP limit that cannot be enforced.** The
  cap is decided from live connections and the address each came from; MTProto
  reports a secret in use and no address at all, so a cap on an MTProto client
  was stored, displayed and applied to nobody. The form now says which of a
  client's inbounds the cap cannot cover, and switches the control off when it
  covers none of them.

- **A WireGuard pre-shared key can be removed.** Clearing the field and saving
  put the old key straight back, because the panel could not tell an emptied
  field from one a partial update had simply left out -- so a pre-shared key
  could be set on a client and never taken off again.
  **What you will notice:** clearing it clears it. Leaving it alone still leaves
  it alone, which is what stops an API call that only changes a quota from
  wiping credentials.

- **A page renders in your language, not in whoever asked last.** The web
  translator was one shared value overwritten by every request -- and the same
  code runs on the subscription server, so each subscriber poll set it to that
  subscriber's language. An admin page could come back in a language nobody on
  it had chosen. It was an unguarded race besides.

- **Turning on a CPU or memory alert now schedules the watchdog, and changing
  the Telegram report time now moves the report.** Both were scheduled once when
  the panel started, from the settings as they were then, and nothing touched
  them again -- so enabling an alert produced no alerts at all until a restart,
  and a new report time was saved, confirmed, and ignored. Every settings save
  puts those schedules where the settings say.

- **An inbound tag can no longer collide with a routing front's.** Naming an
  inbound `peg1` was accepted, and the config then held two inbounds with that
  tag -- which the core refuses outright, taking every inbound of every protocol
  down at the next restart. Those names are reserved now, and the refusal says
  so. Near misses like `peg007` stay yours.

- **Disabling an inbound no longer renumbers its routing egress.** An inbound
  with a routing rule gets a small internal egress, and everything about it --
  its routing table, its network device name -- is derived from an id drawn from
  a pool of 999. Disabling the inbound retired that egress and re-enabling it
  minted a new one, so every toggle renamed the device, moved the table, and
  spent another id from the pool. The egress now sleeps with the inbound and is
  still cleaned up once the inbound is back and no rule needs it.

- **A client cannot be attached to an inbound that serves none.** The panel's
  own pickers already leave HTTP, mixed, tunnel and TUN inbounds out, but the
  API and the Telegram bot accepted a client on one: it was stored, listed on
  the clients page, and provisioned nowhere -- a client that can never connect,
  with nothing saying why. Refused at the save now, naming the protocol.

- **The address list for a client with no IP limit stops forgetting.** For a
  client the limit does not apply to, the panel replaced its whole address list
  with whatever the last poll caught, while a limited client keeps the last 30
  minutes. Two clients side by side showed lists that meant different things,
  with nothing to say so.

- **A "direct" or "block" routing rule stays one when you open it.** Reopening
  such a rule in the editor and saving it unchanged turned it into an ordinary
  outbound rule naming the literal tag. On the stock template that resolves to
  the same thing; on any template without outbounds by those names, the rule
  ended up pointing at an outbound that does not exist.

- **Two host switches that did nothing are gone.** "Mihomo X25519" and "Shuffle
  host" were stored and shown, and read by no part of the panel, the link
  builders or the docs -- toggling either changed nothing about your
  subscription. Anything you set with them is still stored and is not cleared by
  saving a host now.

- **Turning on two-factor authentication no longer reports a failed save that
  succeeded.** Signing out your other sessions happens after the settings are
  already stored, and a failure there was reported as the whole save failing --
  so you saved again, the second save found 2FA already on, and the sign-out
  never ran at all. Those sessions stayed valid with nothing saying so.
  **What you will notice:** the save reports success with a warning telling you
  the other sessions are still valid.

- **Two saves at once no longer leave other browsers showing the old list.** The
  live-update channel drops a second inbounds or outbounds push that lands
  within 250ms of the first, on the assumption that something else will resend
  it -- true for the traffic counters, which tick, and false for these, which
  only fire on a change. Every other open session kept the stale list until a
  reload. They now get a refresh signal instead of nothing.

- **The panel update no longer says it failed while it is still running.** The
  page gave up after 95 seconds and popped "couldn't confirm the update
  finished", then reloaded -- while the panel itself treats an update as live
  for 20 minutes, which a real one on a small VPS routinely needs. The page now
  waits as long as the panel does.

- **Two transport values that take the whole core down are caught in the form.**
  An mKCP send window below the MTU computes to zero buffers and an httpupgrade
  or xhttp `host` header is refused outright -- in both cases the core rejects
  the ENTIRE configuration, so every inbound of every protocol stops answering
  at the next restart. Both forms offered them, and the only signal was a raw
  error bounced back from the save. Now a message on the field, in the inbound
  and outbound editors alike.

- **A client's own Telegram buttons work when their email is long.** The panel
  hashes a callback payload that would exceed Telegram's limit, and only the
  admin path unhashed it -- so for a client whose email made the payload long,
  their subscription, links and QR buttons matched nothing, answered nothing and
  simply span. Around 47 characters of email was enough.

- **"Reset all traffic" in the Telegram bot answers once, not once per client.**
  It sent a separate message for every client on the box, which Telegram
  rate-limits: on any real panel the run went through but the messages stopped
  part-way, so the admin saw the first few and then silence with no way to tell
  a stalled reset from a finished one.
  **What you will notice:** one message saying how many were reset out of how
  many, with any failures listed in it.

- **A quiet minute no longer swallows the CPU or memory alert behind it.** The
  watchdogs publish a reading every minute whatever it is, and the email
  subscriber spent its one-per-minute allowance before checking the reading
  against your threshold -- so a normal minute used up the alert, and the spike
  that followed was dropped with nothing sent and nothing logged.

- **An MTProto inbound on an IPv6 address starts.** The listen address and port
  were joined with a bare colon, so `2001:db8::1` produced something that is not
  an address at all; the MTProto sidecar refused the config and never came up,
  taking every client of that inbound with it. The listen field is free text and
  the panel documents binding to a specific address there.

- **A Shadowsocks client with no password is refused at the save.** Every other
  protocol's client is checked for its credential; this one was checked for its
  name and its cipher and never for its key. With a legacy cipher nothing
  upstream fills one in either, so the empty password reached the core, which
  refuses that user -- and with it the whole configuration, taking every inbound
  of every protocol down at the next restart.

- **Six labels that were showing their own key now show words.** Four of them
  read like English by accident -- "apply", "disable", "empty", "inbounds" --
  which is exactly why nobody noticed, and none of them had a Persian
  translation. The other two were worse: a failed copy said "copyFail", and 22
  places that report a failed load showed nothing at all before the error, so
  the message began with a bare bracket.

- **The NordVPN server list shows the quiet servers again.** It kept only
  servers whose load was ABOVE 7 percent -- the filter upside down -- so the
  least loaded servers, the ones worth picking, were exactly the ones dropped,
  and a country quiet enough that every server sat at or below 7 came back
  empty. Servers with no load figure were discarded too. The full list is shown
  now, with each server's load on its row.

- **NordVPN calls go through your panel's egress proxy.** They used their own
  direct connection while every other outbound request the panel makes -- the
  WARP integration next to them included -- goes through the configured proxy.
  On a panel that reaches the internet only that way, the whole NordVPN
  integration silently failed.

- **A migration that gives up part-way rolls back instead of committing.** Two
  of its early returns declared their own error variable, so the deferred
  commit-or-rollback saw none and committed a half-migrated set of inbounds --
  the next start then migrated from that half-state.

- **Restarting the panel no longer marks every client as just-modified.** The
  same migration runs on every start, and it restamped every client of every
  VMess/VLESS/Trojan/Shadowsocks/Hysteria inbound each time. Beyond rewriting
  the config on every boot, that timestamp is what decides which side wins when
  a node and the panel disagree about a client -- so a restart could make the
  panel's copy look newer than a node's real edit.

- **The mail transport must be one the panel actually handles.** It was stored
  as free text, so only the dropdown constrained it -- a value set over the API,
  or left behind by an older build, is handled by neither of the two places that
  read it: every send fails with "unknown SMTP encryption type", while the Test
  button falls through to a plain connection and reports success. Now refused at
  the save, naming the field.

- **The panel log view says when the row count is the problem.** Its two
  branches disagreed: the system-log side refused a count outside 1-10000 with a
  message, the panel-log side took whatever it was given and returned an empty
  view for "abc", "0" or a negative number -- in the one place an operator goes
  when something else has already gone wrong.

- **The add-client draft in the Telegram bot reads as text again.** It was
  written in Markdown and sent as HTML, so its asterisks and backticks showed
  literally -- and an email, remark or comment containing `<` made Telegram
  refuse the whole message, so pressing a button in the add-client flow simply
  produced nothing.

- **A `<` in a client name no longer silences the Telegram bot.** Every message
  it sends is parsed as HTML, and the values put into them -- emails, remarks,
  comments -- were not escaped, so one angle bracket anywhere made Telegram
  reject the whole message. The server-usage report, the inbound list and the
  depleted-client alert simply stopped arriving for that client, with the
  failure only in the log.

- **"Send a backup to the Telegram bot" says whether it went.** The endpoint
  answered 200 with an empty body no matter what happened -- and the send is a
  silent no-op when the bot is not running, so a backup that never left the
  machine looked exactly like one delivered to every admin. The published API
  spec documented a JSON response the endpoint never wrote, which also broke any
  client that parsed it.

- **Subscription bodies are marked uncacheable.** The subscription HTML page
  said so; the three formats a VPN client actually fetches -- raw, JSON and
  Clash -- carried no Cache-Control at all, and nothing in front of them
  supplied one. Those are exactly the responses that must not be cached: the
  client reads its remaining traffic and expiry from the header beside the body,
  so a proxy, a CDN or the app's own cache could show a user data they had
  already spent, alongside servers that were removed days ago.

- **The subscription page says which of your configs have ended.** Disabling a
  client -- by hand, by running out of traffic, by expiring -- removes its
  account from the core, so its link cannot connect, and nothing said so.
  **What you will notice:** on the subscription page that entry is dimmed,
  struck through and tagged "Ended". The link itself is still served in every
  format, so the subscription keeps parsing in whichever app you use and a
  renewal brings it straight back.

- **The routing table no longer lets you move a rule the panel will move back.**
  The stats API rule -- the one the traffic counters read -- has to lead, and
  every save puts it back at the top. The table offered it a drag handle
  anyway, and let other rules be dragged above it: the rows redrew in the new
  order, the save quietly restored the old one, and routing is first-match, so
  the order on screen was not the order deciding traffic.
  **What you will notice:** that one row has no drag handle, and will not swap.

- **The dialer-proxy picker stops offering a choice that kills both outbounds.**
  It excluded the outbound being edited, which prevents a one-step loop and
  nothing longer: with B already dialling through A, picking B as A's dialer
  proxy left A→B→A. The core starts, the config is valid, and every
  connection through either one goes nowhere.
  **What you will notice:** any tag whose chain comes back to the outbound you
  are editing is no longer in the list.

- **The installer picks a panel port nothing is already using, and says when the
  panel did not start.** The random port was drawn blind, and `systemctl start`
  returns before a panel can die -- so an occupied port, a bad database URL or a
  broken unit ended with "installation finished, it is running now", an access
  URL that refuses, and the same claim persisted for the login banner to repeat.
  **What you will notice:** a free port; a port you chose yourself is kept, but
  you are told if something is on it; and a failed start prints the last of the
  journal instead of a success message.

- **The updater stops announcing HTTPS it did not set up.** Its access-URL block
  guarded on a variable that every path sets -- including declining SSL and both
  certificate failures -- so it always took the https branch, with the scheme
  hardcoded besides. Skipping SSL was reported as "SSL Certificate: Enabled and
  configured" with an https:// URL that could not connect. The installer was
  repaired for this; the updater had the same bug with a guard that looked like
  the fix.

- **A WireGuard address entered by hand no longer moves every future client.**
  New addresses are allocated from the device's own prefix whenever it covers
  the clients on it, but the search stopped at the first client it looked at --
  so one address outside the prefix, sitting at the head of the list, sent every
  later allocation into a /24 derived from that one client and left the rest of
  the configured range behind.

- **Exporting your routing rules exports all of them.** The file held only the
  rules that arrived with a template; every rule created in the routing dialog
  was left out, and the export menu greyed itself out on that same count -- so a
  panel whose rules were all its own could not export at all. Nothing said so:
  the file was valid, and the gap only showed on the restore.
  **What you will notice:** the export matches the table, operator rules first,
  in the order the router evaluates them.

- **The inbound enable switch follows the panel, not a guess.** It wrote the new
  position onto the row it was holding, which lives in the page's cache -- so
  React repainted nothing, and the refresh that should have repainted it was
  then skipped as a no-op. The switch could sit on the old position while the
  inbound was already off.

- **A subscription whose clients expire on different days stops claiming it
  never expires.** The header apps read for the countdown carries one number,
  and a subscription folding two clients with different deadlines set it to
  zero -- which on the wire means "unlimited". The client app showed no expiry
  at all, right up until access stopped.
  **What you will notice:** the nearest of the deadlines. A client that
  genuinely never expires still makes the whole subscription unlimited, the same
  way one unlimited client already makes its traffic allowance unlimited.

- **An inbound can no longer be saved on a bind nothing can serve.** Port 0 was
  accepted over the API and the Telegram bot -- the form always refused it --
  and the core then bound whatever port the operating system handed it, changing
  on every restart, while the inbound list, every share link and the
  port-conflict check all said 0. The listen address had no rule at all, so a
  typo'd one was stored too, on an inbound that could then never come up.
  **What you will notice:** both are refused, naming the field. A listen that is
  a unix socket path (`/run/xray/in.sock`, or `@name` for an abstract one) still
  takes no port, exactly as that field's hint says. An update that omits the port
  is refused rather than silently moving the inbound to a random one.

- **The host form checks the fields it always meant to.** Its port, tags, server
  description and VLESS route were declared with rules and then submitted
  without running them, because the form had no resolver attached. A malformed
  VLESS route in particular was stored and then ignored, leaving a link that
  looks configured and routes nowhere.

- **Changing an inbound's protocol checks the clients it already has.** The
  switch was applied to the stored row without asking the new protocol whether
  it could serve them, so, for instance, a VLESS inbound turned into a kernel
  WireGuard one came up with peers the kernel has no key for -- every client of
  it offline, and the save reported as a success.
  **What you will notice:** the switch is refused, naming what the clients are
  missing. Editing anything else on an inbound is unaffected.

- **The egress pre-flight stops condemning the panel's own routing fronts.** A
  front the panel provisions has no target of its own -- the target comes from
  the rule that names it -- but the check asked every enabled front to resolve
  one, so each of yours was reported as "contained rather than routed". Fronts
  you created yourself are still checked.

- **Client names with invisible whitespace are caught in the form.** A name or
  subscription id pasted from a document or chat app often carries a
  non-breaking space. The server refuses those; the form did not, so the save
  bounced back as a raw error with nothing marking the field.

- **Bulk-add says why it did nothing.** Setting a last number below the first
  produced no names, and the dialog just sat there -- no message, no field error.

- **The AmneziaWG installer prints a command that exists.** On a machine without
  kernel headers it told you to run `p-ui awg install`, which is not a command;
  the panel's own installers print the right one.

- **A WireGuard client is never handed the inbound's own device address.** The
  allocator skipped addresses other inbounds used but not the address of the
  device it was allocating for, so on a device at, say, `10.0.0.5/24` the fourth
  client created got `10.0.0.5` -- the same address the server answers on. That
  client's return traffic went to the server itself instead of into the tunnel,
  silently.
  **What you will notice:** that address is skipped. Existing clients are not
  renumbered; if one already holds the device address, change it by hand.

- **SMTP errors are readable again.** Every failure message from the mail
  settings was shown as a raw key like `pages.settings.pages.settings.smtpErrorAuth`
  -- the panel added a prefix the code had already added, so nothing resolved.
  **What you will notice:** actual sentences.

- **A mail relay that refuses your password over an unencrypted connection now
  says so.** With encryption set to "none" against a server offering no
  STARTTLS, Go refuses to send the password and reports only "unencrypted
  connection", which names neither SMTP nor the cause. That is what every expiry
  notice and login alert failed with.
  **What you will notice:** the message explains that the server offered no
  STARTTLS and suggests using TLS or a relay that supports it.

- **WireGuard addresses no longer collide with networks the server itself is
  on.** The panel decided which subnets were free by looking only at its own
  inbounds -- but the thing being handed out is the server's single routing
  table, and your LAN interface, docker's bridge or a private-network NIC are in
  it without any inbound saying so. A tunnel subnet overlapping one of those
  saved cleanly and then installed a route that captured that network's traffic.
  **What you will notice:** creating an inbound on a subnet the server already
  holds is refused, naming the interface that holds it. Client addresses are
  allocated AROUND such ranges rather than refused. If the server's addresses
  cannot be read -- a transient failure, or a non-Linux box -- nothing is refused
  and behaviour is exactly as before. Existing inbounds are not re-checked, so
  one that already overlaps stays editable.

- **Routing rules now wait for Save.** Adding, editing, deleting or reordering a
  routing rule took effect the moment you clicked OK -- it was written and, since
  every one of those writes schedules a reload, it reached live traffic within
  the next reconcile. Routing is first-match, so moving a rule up the list was
  itself a live routing change. The Save button meanwhile stayed dark, because it
  only watched the Xray template and these rules are not in it, so there was no
  review step and no way to ask for one.
  **What you will notice:** rule changes are held until you press Save, exactly
  like the rest of the Xray page, and Save lights up while you have unsaved ones.
  Leaving the page without saving discards them.

- **Routing attributes survive being edited.** `attrs` is the one rule criterion
  stored as a set of key/value pairs rather than a list, and the editor flattened
  it to the literal text `[object Object]` on the way in -- then split that into
  one row per character. Saving stored the mangled text, so whatever you had
  configured was replaced by junk the moment you opened the rule.
  **What you will notice:** attribute rules open and save unchanged. A stored
  value that is not a set of pairs is ignored instead of expanded.

- **A routing attribute pattern that cannot compile is refused when you save
  it.** Xray only compiles these patterns when a packet arrives, so an unclosed
  bracket saved cleanly and then failed inside the running router, on live
  traffic, with nothing tying the two together.

- **Attribute rules are no longer offered for L3 (device) subjects.** Xray reads
  attributes from sniffed metadata, and the inbound the panel builds for those
  subjects does no sniffing -- so such a rule saved, displayed as active, and
  matched nothing. It is now shown as unavailable with the reason, alongside the
  other criteria that cannot apply there.

- **The routing page stops offering an MTProto inbound whose Outbound is already
  forced.** When such an inbound has an Outbound set in its own form, the panel
  applies that above every routing rule -- so a rule naming it could never win.
  The subject picker offered it anyway, and the rule you wrote sat there looking
  active while doing nothing.
  **What you will notice:** the subject is shown as unavailable with the reason.
  No traffic changes route; this only stops the picker offering a rule that
  cannot fire. Clear the inbound's own Outbound field to route it by rules.

- **The panel stops treating its own AmneziaWG uplink devices as somebody
  else's.** It recognised only one naming scheme for the devices it dials out
  through, and AmneziaWG uses a second. A healthy AmneziaWG uplink therefore read
  as foreign: the pre-flight refused it as a squatter on its own routing table,
  and disabling it left its route behind as though it belonged to another
  program.
  **What you will notice:** AmneziaWG uplinks pass the pre-flight and are cleaned
  up properly when disabled.

- **The panel's own outbound requests re-check the address they actually dial.**
  Three clients -- the traffic-report webhook, the Telegram API client and the
  subscription fetcher -- validated an administrator-supplied URL against the
  private-address policy and then dialled with an unchecked resolver, so a
  hostname that answered publicly at check time and privately at dial time
  reached the panel's own loopback or LAN. The subscription fetcher also
  followed redirects to addresses nobody had checked at all.
  **What you will notice:** nothing, unless you were pointing one of these at a
  private address, which was already refused. Telegram traffic through a
  configured proxy is deliberately unchanged.

- **Resetting traffic no longer stops an MTProto inbound the panel cannot
  read.** Before resetting, the panel reconciled its MTProto sidecars -- and that
  reconcile stops anything absent from the desired list. An inbound whose stored
  settings will not parse is skipped when that list is built, while its sidecar
  is running perfectly well, so a reset killed it and nothing brought it back.
  Renewing a client goes through this path.
  **What you will notice:** a reset converges the inbounds it can read and stops
  nothing. An inbound with unreadable settings keeps serving.

- **The one-time routing conversion no longer hoists rules above a private-IP
  block.** Converting an existing template into the new routing rules imports a
  leading run of rules and leaves the rest in place -- but it stepped over
  certain rules without stopping, including a `geoip:private` block. Everything
  below such a rule was then lifted ABOVE it, and since routing is first-match,
  LAN traffic could start matching a rule meant for the internet.
  **What you will notice:** on a panel that has not converted yet, the import
  stops at the private block and everything from there down stays exactly where
  you put it. Already-converted panels are not re-run.

- **Renaming or deleting an inbound now updates the routing template even if
  that template was already invalid.** The update was saved through the full
  config validator, which re-checks the WHOLE stored template -- so one
  pre-existing problem in it, from a hand edit or an older build, made every
  rename and every deletion silently fail to reach the routing rules. The rules
  went on naming an inbound tag that no longer existed, and the only sign was a
  warning in the log.
  **What you will notice:** the tag update lands. It cannot make a template worse
  -- it only swaps one tag for another -- and it stops routing rules pointing at
  inbounds you renamed or removed.

- **Changing the WARP IP no longer loses your WARP+ licence.** Rotating
  registered a new device, which overwrote the stored WARP+ key with the new
  device's free one, and only then tried to re-apply yours -- a step allowed to
  fail with a warning. Cloudflare rate-limits registrations, so a rotation
  retried a couple of times is exactly when that fails, and the paid key was
  then gone from the panel with nothing left to retry from.
  **What you will notice:** your licence stays in the panel even when the
  re-apply fails, so you can rotate again later.

- **"Move up" on the first template routing rule no longer reorders two other
  rules.** The routing table shows your own rules and the template's as one
  list, but they are stored separately. Moving the first template rule up used
  the neighbouring OPERATOR rule's position as though it were a template
  position, and swapped two template rules you never touched. Routing is
  first-match, so that changes which rule decides your traffic, and nothing said
  it had happened.
  **What you will notice:** the move is refused at the boundary between the two
  groups, which is what dragging already did.

- **A Telegram backup that failed now says so.** The message always opened by
  stating the hostname and the backup time, and then said nothing at all if the
  database dump could not be produced or the upload failed. An admin saw a
  header naming a time and assumed the file was on its way -- and found out
  otherwise when they needed to restore.
  **What you will notice:** a warning in the chat when the dump could not be
  built or the upload did not go through. A successful backup is unchanged.

- **LDAP no longer re-enables clients on inbounds it was not given.** The sync
  decided who to enable from a list of every client on the panel, without
  checking whether the client was on any of the inbounds you put under LDAP
  control. So disabling such a client by hand was undone on the next tick --
  every minute, indefinitely, with no way to stop it short of turning LDAP off.
  **What you will notice:** a client is only re-enabled on an inbound it is
  actually on. Disabling stays panel-wide deliberately: narrowing that would
  leave an off-boarded account working wherever it sits outside your configured
  inbounds. A client list that cannot be read is now logged instead of silently
  narrowing what LDAP governs.

- **Enabling two-factor authentication no longer says it is done before it is.**
  Entering a valid code showed "Two-factor authentication has been successfully
  established" -- but that step only ticks a box in the unsaved form. Nothing had
  been sent to the server, and leaving the page without pressing Save left 2FA
  off, having been told it was on.
  **What you will notice:** the message now says the code was verified and asks
  you to press Save.

- **The "Test SMTP" button now tests the connection the panel actually makes.**
  Sending always upgrades to STARTTLS when the server offers it, whatever
  encryption you selected -- but the test button only did so when you had picked
  "STARTTLS" explicitly. Against a relay that requires the upgrade before
  accepting a login, the test failed while sending worked; where the upgrade
  itself was broken, the test passed while every message failed.
  **What you will notice:** the button agrees with what sending does. Implicit
  TLS is unchanged.

- **A bad LDAP sync schedule no longer switches LDAP off in silence.** The
  scheduler expects six fields (it counts seconds), while every cron reference —
  and the settings field itself — invites the five-field form. A five-field spec
  was rejected and the error thrown away, so the sync was never scheduled at
  all. Off-boarding fails open when it does not run, which means every account
  you disabled in the directory kept working on the panel indefinitely, with
  nothing anywhere to say so.
  **What you will notice:** an unparseable schedule falls back to the default of
  every minute and logs an error naming your spec and where to fix it.

- **An inbound that runs out of quota now really stops serving.** It was flipped
  off in the panel and dropped from the core by tag -- but when that drop failed,
  nothing else happened. The listener stayed up with every session on it still
  running, and the fallback reload was never scheduled, because only disabled
  CLIENTS ever triggered it and disabled INBOUNDS did not.
  **What you will notice:** a depleted inbound served by Xray now schedules the
  reload that removes it. MTProto and kernel-WireGuard inbounds deliberately do
  not -- they are not in the Xray config, so restarting Xray for them would drop
  every live Xray session and change nothing. Unaffected if you have "Restart
  Xray After Auto Disable" switched off.

- **Disabling an inbound now takes effect even if removing it from the core
  fails.** The removal's failure was ignored entirely: the switch was saved,
  the panel reported success, and the listener stayed up with every current
  client still connected to an inbound shown as off. Nothing retried, because
  the fallback that rebuilds the config from the database was never triggered.
  **What you will notice:** such a failure now schedules a reload, so the inbound
  really does stop serving.

- **Disabling an outbound subscription now takes effect.** Editing a
  subscription -- turning it off, changing its prepend flag, reordering it --
  saved the change and never told Xray to reload, so its outbounds stayed in the
  running core and kept serving traffic indefinitely. Deleting one always
  signalled correctly; only editing did not.
  **What you will notice:** the change applies at the next reload, the same as
  deleting one already did.

- **Two clients on one inbound can no longer share the credential that
  identifies them.** The panel checked email, subscription id and WireGuard keys
  for duplicates, and nothing else -- but Xray keys its user table on the UUID
  for VLESS and VMess, on the password for Trojan, on the auth string for
  Hysteria. Two clients with the same value were ONE user to the core: both
  showed in the panel with their own quotas, all the traffic landed on
  whichever one the core kept, and deleting the other took the survivor's
  access down with it.
  **What you will notice:** creating or editing a client into a duplicate is
  refused, naming the client that already holds the value. Clients already in
  that state are untouched and stay editable -- only an edit that CHANGES the
  credential is checked.

- **"Keep SNI blank" on a host now actually keeps it blank.** The switch was a
  no-op on every TLS path: the panel expressed "blank" by simply not writing an
  SNI, which every link generator read as "no override" -- and each of them had
  already written the inbound's own SNI, so the value you flipped the switch to
  hide was published anyway. Raw links, Clash and JSON were all affected, for
  VMess, VLESS, Trojan, Shadowsocks and Hysteria.
  **What you will notice:** subscriptions for such a host carry no SNI. Hosts
  that set their own SNI are unchanged.

- **Excluding every host from a subscription format now removes the inbound from
  it, instead of publishing your origin address.** "No host applies here" and
  "this inbound has no hosts at all" were treated as the same thing, so closing
  every host to, say, Clash did not take the inbound out of the Clash
  subscription -- it fell back to the inbound's own address, which is exactly
  what the host was added to hide. Raw, JSON and Clash were all affected.
  **What you will notice:** such an inbound no longer appears in that format at
  all. Traffic totals in the subscription header are unchanged -- the clients are
  still counted.

- **Starting Xray from cold no longer reports success for a config the core
  refuses.** The config pre-flight guarded restarts of a RUNNING core only. When
  Xray was not running -- panel boot, crash recovery, `p-ui restart`, the first
  start after an install -- the panel launched the core, saw the process spawn,
  and reported success. The core then read the config, refused it and exited, so
  the Restart button said it worked while the panel served nothing.
  **What you will notice:** the failure is reported immediately, carrying Xray's
  own explanation of what it did not like.

- **Creating a tun inbound no longer binds every outbound to one network
  interface.** The panel wrote `autoOutboundsInterface: "auto"` into every tun
  inbound it saved, whether or not you touched the field. Xray acts on that by
  registering a process-wide dialer controller, so one tun inbound rewrote the
  source interface of every outbound on the server.
  **What you will notice:** new tun inbounds leave the field unset, and Xray
  decides for itself based on the routing table -- which is what it does when
  the field is absent. Existing inbounds are NOT changed: a stored "auto" is
  indistinguishable from one you typed, so it is left alone. Clear it by hand if
  you did not mean it.

- **Viewing the Xray config no longer deletes routing state the running core is
  using.** Building the config ends by retiring egress "fronts" that no routing
  rule names any more -- which deletes the database row and tears down its `ip
  rule`, routing table and blackhole. Two things that only ever READ the config
  ran that too: the "view config" button, and the pre-flight that checks a newly
  downloaded Xray binary. So opening a settings page could tear down kernel
  state the live core was still forwarding through.
  **What you will notice:** nothing, unless you were hitting this -- retiring now
  happens only on the path that actually applies the config, which is where it
  always belonged.

- **Simultaneous inbound changes no longer delete each other from the running
  core.** Applying a change read the live config, spliced one inbound into a
  copy, and pushed the whole thing back. Two of those overlapping both started
  from the same snapshot, so the second push carried a version that had never
  seen the first -- and the core deleted the inbound that had just been added.
  The panel reported success for both. Driving sixteen at once, fifteen
  disappeared from the running core while the database still listed them.
  **What you will notice:** changes made at the same time -- by two admins, or by
  you while the auto-disable job runs -- all take effect. Nothing else changes.

- **Two saves can no longer both claim the same port.** The port check read
  committed rows and the save committed later, with nothing holding the gap, so
  two admins saving at once -- or one double-clicking Save -- could both be told
  the port was free. Both rows were kept and only the tag was disambiguated, so
  the collision hid behind a plausible name until Xray refused to start the
  second listener and the whole core went down.
  **What you will notice:** the second save is refused with the ordinary "port
  already used by inbound" message instead of appearing to succeed.

- **An MTProto bridge-port collision no longer takes the whole Xray core down.**
  Each routed MTProto inbound gets a loopback bridge on a port the panel picks,
  and that port was checked against nothing. If anything else already held it,
  Xray failed to start -- after the running process had been stopped -- so one
  MTProto inbound with an unlucky port number took every inbound on the server
  offline.
  **What you will notice:** the clash is detected while the config is built. That
  one inbound's Telegram traffic will not work until you change its port, and
  the log says which inbound is holding it; everything else keeps running. A
  UDP-only neighbour on the same port number is not a clash and still bridges.

- **A `null` section in the Xray template no longer stops the panel booting.**
  Writing `"policy": null` or `"api": null` into the template -- which the editor
  accepted, and which some tools emit for an empty section -- crashed the panel
  on startup. Because that happens before the web server is up, the panel could
  not be reached to undo the template, and systemd restarted it into a loop.
  **What you will notice:** such a section is now left exactly as you stored it
  and the panel starts. Saving one no longer returns a 500 either.

- **A WireGuard outbound keeps the UDP masks you configure for it.** The form
  offered a Final Mask editor for WireGuard outbounds and the save silently
  emitted only `sockopt`, dropping every mask. Xray applies those masks on a
  WireGuard outbound, so the panel was discarding a setting the core would have
  honoured.
  **What you will notice:** masks set on a WireGuard outbound now reach the
  generated config. If you had configured them and wondered why nothing changed,
  they will start taking effect after the next restart.

- **You can reset the traffic of the clients you selected.** The panel offered
  "reset this one client" on a row and "reset all client traffic" in the menu,
  with nothing in between -- so resetting forty renewed subscriptions meant
  either forty clicks or zeroing every client on the panel. The API for it had
  shipped long ago and nothing in the interface called it.
  **What you will notice:** a "Reset Traffic for Selected" item in the bulk menu
  whenever rows are selected. It zeroes their counters and re-enables any client
  that was disabled for reaching its quota, exactly as the single-client reset
  does.

- **Opening the Xray log viewer no longer reads the whole access log into
  memory.** Every line that survived the filters was collected first and the row
  count you asked for was applied only at the end, so asking for the newest 20
  lines parsed the entire file. Xray rotates that file at 64 MiB and an operator
  with debug logging reaches it in hours; on a small VPS the panel could be
  killed for opening a modal.
  **What you will notice:** the viewer is unchanged in what it shows. A row
  count outside 1-10000 is now refused, the same range the application-log
  viewer already enforces -- previously a count of 0 or a non-number silently
  returned nothing after reading the whole file, and a negative count crashed
  the request.

- **IPv6 WireGuard inbounds can hand out more than one working client.** Client
  addresses were always rendered with a `/32` host suffix, which is meaningful
  only for IPv4. On an inbound whose device address is IPv6, every client got a
  96-bit-wide network instead of a host address, so the kernel refused every
  peer after the first, installed a route for that whole network over the
  device, and dropped the survivor from bandwidth shaping. The `.conf` handed to
  the customer carried the wrong address too.
  **What you will notice:** IPv6 inbounds allocate `/128` host addresses and
  work. IPv4 is byte-for-byte unchanged. Clients already holding a wrong address
  are NOT rewritten -- a server must not renumber an address a customer already
  has in their config; edit those by hand.

- **The client form now says when an Auto Renew period can never run.** Auto
  Renew moves a client's expiry date forward -- so a client given a renew
  period and no expiry date has nothing to renew. It is disabled the moment its
  quota runs out and stays that way: nothing renews it, and the depleted-client
  cleanup skips it too because that only removes clients with no renew period.
  The form took the number without a word.
  **What you will notice:** typing an Auto Renew value with no expiry date set
  shows a note under the field explaining that it will not run. Nothing is
  refused, and existing clients are untouched.

- **The installer no longer reports HTTPS after a certificate failed to issue.**
  When Let's Encrypt could not issue for your domain or your IP, the installer
  printed the red failure line and then went on to print an `https://` access
  URL, state "SSL Certificate: Enabled and configured", and write `scheme=https`
  into `/etc/p-ui/install-result.env` for cloud-init and the login banner to
  repeat -- while the panel was serving plain HTTP and that URL refused to
  connect.
  **What you will notice:** after a failed certificate the URL is `http://` and
  the summary says the panel is HTTP-only. A certificate that issues is
  unchanged.

- **An update started from the panel no longer hangs forever when the server IP
  cannot be detected.** The scripts asked for it at a prompt, in a loop with no
  way out for a read that fails. The panel runs update.sh detached with no
  terminal, so at that point the read failed instantly and the loop spun. The
  panel itself had already restarted and kept serving, which is why this looked
  like nothing at all: the update simply never finished, the progress indicator
  stayed on forever, and fail2ban setup and the rest of the post-update steps
  never ran.
  **What you will notice:** an unattended update continues without the IP and
  says so. An update you run yourself in a terminal is unchanged.

- **Re-running the PostgreSQL setup no longer changes the database password out
  from under a working panel.** Menu option 25 -> 1 offers to "run setup anyway"
  on a machine that already has PostgreSQL, describing itself as ensuring the
  database and user exist. It also set a brand-new password on the role, before
  asking whether to point the panel at the new connection string. Declining left
  the panel running on connections it had already opened and unable to log in at
  its next restart -- an outage that arrived hours later with no obvious cause.
  **What you will notice:** when the role already exists and the panel is
  connecting with it, the password is left alone and the run reports that.
  Declining the prompt now also warns what declining costs if a password was set.

- **A malformed client entry no longer stops the panel from upgrading.** If an
  inbound's stored settings held a `clients` entry that was not an object — a
  `null`, a bare string, a number, which a hand-edited row, a restored dump or a
  migration from another panel can all produce — `p-ui migrate` panicked. Both
  `install.sh` and `update.sh` run that command and stop when it fails, so one
  such row blocked the upgrade of a panel that was serving traffic.
  **What you will notice:** the migration drops those entries and logs each one
  with its inbound id and position. That inbound's client list also becomes
  readable again — a non-object entry breaks every client read of the inbound,
  so pages that were failing to load its clients start working.

- **Typing a subscription id into bulk-add no longer collapses the batch to one
  client.** The id was stamped onto every client in the batch, and since the
  panel refuses a duplicate subscription id, the first one claimed it and the
  rest were skipped — asking for 50 clients produced 1. Leaving the field alone
  always worked, which is why this went unnoticed.
  **What you will notice:** a typed id is used when you are creating exactly one
  client, and each client of a batch gets its own. The failure list you would
  have seen before does not appear, because nothing fails now.

- **Opening an exit the editor cannot edit now says so.** Only one kind of exit
  has an edit form. Opening any other — a panel-owned front, or an AmneziaWG
  uplink — showed an empty form whose OK saved nothing and closed as if it had
  worked.
  **What you will notice:** OK is disabled and the dialog explains that this
  kind of exit takes its settings from the uplink or front it belongs to.

- **A WireGuard client attached to two kernel-WireGuard inbounds can be edited
  again.** Each inbound gives such a client its own tunnel address and its own
  keypair, but the client row holds one of each, written by whichever inbound
  saved last. The editor read that row and sent it to both, so the second
  inbound refused the save — *allowedIPs entry already used by another client* —
  where the other client was the same client's own copy on the first inbound.
  Renaming it, changing its quota, editing its comment: all refused, on a client
  that was working fine.
  **What you will notice:** those edits go through. Each inbound keeps its own
  address and keys, so nothing a client has already been given stops working.

- **Flipping a client's enable switch no longer deletes its MTProto ad tag.**
  The toggle rebuilt the whole client and sent it to the editor's endpoint,
  where an absent ad tag means *clear it* — correct for the editor, where you
  need to be able to clear one, and destructive for a switch that never carried
  the value in the first place. One flip removed the tag from every inbound the
  client was attached to, with nothing to undo it.
  **What you will notice:** the switch keeps the ad tag. It also gets faster, as
  it no longer fetches the whole client first. **Worth checking by hand:** an
  MTProto client whose ad tag went missing after someone toggled it needs the
  tag setting again — the panel has no record of what it was.

- **A config that lost your routing rules is no longer deployed.** If the panel
  could not compile your rules — a database error, an exit that stopped
  resolving — it logged a warning and shipped the config anyway. That config is
  perfectly valid to the core, so the restart *succeeded* and the panel looked
  healthy while every rule you wrote was absent: kernel WireGuard inbounds went
  dark, and everything else fell through to the template, leaving via this
  server's own address rather than the uplink you routed it to.
  **What you will notice:** if the panel cannot compile your rules it now
  refuses to build the config at all. The running core keeps serving — nothing
  is stopped — and it retries every 30 seconds, so a transient database problem
  heals itself. A persistent one now means Xray does not start, which is
  deliberate: no proxy is better than a proxy routing your clients somewhere
  they did not choose.

- **Per-client traffic is counted on every policy level, not just the stock
  one.** The panel forced `statsUserOnline` on but never the two traffic
  counters, and it *created* the broken case: given a policy block with no
  level 0, it inserted an empty one and enabled only that flag. The core then
  never registers the counters at all — so the panel reads no data rather than
  zero.
  **What you will notice:** nothing, on a stock config. If you have edited the
  `policy` block in your Xray template and your clients' usage had stopped
  moving — quotas never depleting, charts flat — this was why, and it starts
  counting again on the next restart. **Traffic that went uncounted is gone**;
  the panel cannot recover what the core never recorded.

- **A routing rule that stopped working now says so on the Routing page.** Saving
  a rule the router cannot realise has been refused for a while, but a rule that
  was fine when you saved it can go inert later — the inbound it names gets
  disabled, its exit stops resolving, the Xray template changes under it. Those
  arrive after the only check that existed, and the only trace was a log line.
  A rule in that state now carries a warning beside its switch, with the reason,
  in your language. The rule is still switched on; it is simply not being
  applied, and those are two different facts.
  **What you will notice:** a warning triangle on affected rules. It appears
  after the first config generation following a restart, and clears by itself
  when the rule becomes realisable again.

- **A Host's Final Mask can now set its own Hysteria2 obfuscation password.**
  The Hosts page has always offered the field and told you it saved, but every
  link kept the inbound's password: the raw generator never looked at a host's
  masks, and the Clash one merged them and then stopped at the inbound's entry.
  **What you will notice:** if you set an obfs password on a Host row and it
  never took effect, it will now, on the next fetch. A host that sets none still
  inherits the inbound's. Nothing changes for an inbound with no Host rows, or
  for hosts that never set the field.

- **A REALITY client's `sni` and `sid` stop changing on every subscription
  fetch.** With more than one `serverName` or `shortId` configured, the panel
  drew a fresh random member on every render, so a client polling its
  subscription got a different link each time with nothing on the server having
  changed. Every link API did it — `/sub/`, `/json/`, and the three
  `/panel/api/…/links` endpoints — and so did the panel's own QR and Export,
  which re-rolled each time you opened them.
  Each client now derives one member and keeps it, the way `spx` already did.
  Different clients still land on different names, so a decoy domain that gets
  blocked costs a fraction of your users rather than all of them.
  **What you will notice:** **every REALITY link in circulation changes once.**
  Nothing breaks — the new value is a member of the same list, and the server
  accepts any member — but a saved QR code or a pasted link will no longer
  match what the panel shows, and clients will pick up the new one on their next
  fetch. If you want every client on one name instead, set
  `realitySettings.settings.serverName`; that has always won over the list.
  Inbounds with a single `serverName` and `shortId` are unaffected.

- **A host's overrides no longer leak into the host rows after it.** On an
  inbound with more than one Host row, the Clash and JSON subscriptions gave
  every row the *first* row's path, Host header and REALITY SNI — whether or
  not that row set any of them. The raw subscription beside it was correct,
  which is why this read as a server fault rather than a subscription one.
  **What you will notice:** if you run several CDN or domain fronts on one
  inbound and your Clash or JSON subscribers could only reach the first of
  them, this was why. They start working on the next fetch. Raw subscribers
  were never affected and see no change.

- **A restart that would leave you with no proxy is now refused.** xray-core
  loads its config whole: one malformed inbound, routing rule or outbound aborts
  the load, and the panel had already stopped the core it was replacing. A
  single bad save ended every inbound of every protocol on the box, the bad row
  was saved anyway, and every retry — including the watchdog's — reproduced it.
  The panel now asks the pinned binary's own `-test` loader before it stops
  anything, keeps the running core when the answer is no, and asks in the same
  place on the two other paths that reach the core: the hot apply, and enabling
  a row that was saved while disabled.
  **What you will notice:** a save or a restart can now come back with the
  core's own refusal text instead of appearing to work. The proxy stays up while
  you fix it. Being *unable* to ask — no binary, no permission, a timeout — is
  never treated as a refusal, so this can not become the reason a restart cannot
  happen. It does not run on a cold start, where there is nothing to protect.

- **A host's Split download address can be saved.** The column, the form tab and
  the subscription renderer have all existed since the feature shipped; the one
  write path in between never learned the field, so the value was dropped on the
  way in. Every host anyone has created has had it empty. An operator set it,
  was told it saved, reopened the tab to find it blank, and every subscription
  kept handing out the inbound's original download endpoint — the exact mismatch
  the feature exists to fix.
  **What you will notice:** the field persists. If you set one before and gave
  up, set it again.

- **A WireGuard or AmneziaWG uplink exit can be created from the Outbounds
  page.** Every attempt failed with a database error. The form sent an ingress
  inbound of `0` where it meant "none", and `0` is not a row any inbound can
  ever have, so the foreign key refused it. This was the only way to author an
  uplink exit in the panel, so the feature has been unusable from the UI since
  the constraint landed.
  **What you will notice:** it works. Nothing to repair — no such exit was ever
  stored.

- **A routing rule with a criterion the core cannot read is refused when you
  save it.** A port of `abc`, a malformed CIDR, a bad domain matcher: all were
  copied into the config verbatim, and xray-core refuses the whole routing
  section over one of them — taking every client on the box down and re-arming
  the pending restart to retry the same broken config every tick.
  **What you will notice:** the rule editor rejects the value with a reason
  instead of accepting it. Validation runs through xray-core's own rule parser,
  not a hand-written imitation, so what the panel accepts is exactly what the
  core accepts. **A rule already saved with a bad criterion is still stored** —
  this stops new ones; open and re-save any rule you are unsure of.

- **A VLESS client whose flow is `xtls-rprx-vision-udp443` is stored as
  `xtls-rprx-vision`.** That value is a *client*-side flow: the client truncates
  it to sixteen bytes before it goes on the wire, and the inbound then refuses
  any account whose stored flow is not what arrived. The panel accepted it and
  reported the client added; that client could never connect.
  **What you will notice:** the flow shown for such a client changes to the
  value that works. This is the panel rewriting something you saved, which it
  normally will not do — the exception is made because the stored value has no
  working meaning on an inbound and the alternative is a client that silently
  never connects.

- **A host's overrides now reach a Clash subscription for a Hysteria inbound.**
  They already reached the raw and JSON renderings. The Clash one rebuilt the
  entry from the inbound as stored and ignored the host entirely, so its SNI,
  ALPN, fingerprint, Salamander obfuscation password and UDP hop range were all
  silently dropped — only the address and port came through.
  **What you will notice:** if you have a Hysteria inbound with a host row and
  Clash subscribers, their config changes to match the host. That is the
  configuration you asked for; it may differ from what those clients have been
  using.

- **The API reference no longer claims a subscription lists only enabled
  clients.** It always listed every client on an *enabled inbound*, disabled
  ones included; only the inbound is filtered on `enable`, never the client.
  That is deliberate — a disabled client's credential is stripped from the
  running core, so the link it holds is already dead, and keeping the link
  visible is what lets the panel show it and lets `{{STATUS}}` label it as
  disabled. Nothing changed in what is served; three summaries now say what has
  always happened.

- **The Shadowsocks inbound form no longer offers `ivCheck`.** The switch wrote
  a key nothing has ever read: the core's JSON config parser has no `ivCheck`
  field for a Shadowsocks inbound, and the only trace of it left in the core is
  a protobuf field with no readers. Turning it on or off changed nothing.
  **What you will notice:** the control is gone. An inbound saved with the key
  still carries it in its stored settings until the next time you save that
  inbound, at which point it is dropped — there is no migration, and none is
  needed, because nothing reads it either way.

- **A subscription id containing `#`, `?` or `%` is refused when you set it.**
  The id becomes a path segment of the subscription URL, and those three break
  it: `#` truncates the address to a fragment, `?` starts a query, and `%` opens
  an escape the router decodes before looking the id up. A client with one got a
  404 for a subscription that exists.
  **What you will notice:** almost certainly nothing — generated ids are random
  and nothing unprivileged sets one. If you have a client already holding such
  an id, it stays editable and importable on purpose; only setting a *new* one
  is refused, so you can still open that client and correct it. `&` and `+` are
  still allowed: they are legal in a path segment and have always worked.

- **An outbound subscription can no longer take a tag you already use.** The
  three sources of outbounds — your template, your subscriptions, and the ones
  the panel generates — were concatenated with no check that a tag appeared once.
  Xray refuses a duplicate tag outright, and because subscriptions are listed
  first, the resolution that did happen handed *your* tag to a third-party
  server: every routing rule naming it then sent traffic somewhere you never
  chose.
  **What you will notice:** your own outbounds keep their tags. A subscription
  outbound that wanted one is dropped and the log says which:

  ```
  journalctl -u p-ui | grep "is already held"
  ```

  If you see that line, rename the tag prefix on the subscription — the dropped
  outbound is unreachable until you do.

- **A fallback child on a unix socket gets its master's address again.** An
  inbound listening on an `@abstract-socket` has no address a client can reach,
  so the subscription substitutes the address of the inbound that fronts it.
  For a configuration written before the panel had a fallbacks table, that
  lookup searched the stored settings — using SQL that only SQLite understands,
  on a panel that only runs PostgreSQL. It failed on every call and the error
  was discarded, so those clients were handed `@socket` as their server address.
  **What you will notice:** nothing, unless you have an inbound listening on a
  unix socket that is fronted through another inbound's stored `fallbacks`
  rather than through the Fallbacks card. Those links start working. The lookup
  now logs when it fails for any reason other than finding nothing.

- **A Shadowsocks-2022 inbound whose server key is too short for its method is
  refused when you save it.** The key size is fixed by the method — 16 bytes for
  `aes-128-gcm`, 32 for `aes-256-gcm` and `chacha20-poly1305` — so switching an
  existing inbound from the 128 method to a 256 one left a key Xray rejects, and
  it rejects the *whole* configuration over it.
  **What you will notice:** that switch now fails with a reason instead of
  succeeding and stopping the panel at its next cold start. The key is **not**
  regenerated for you, deliberately: that would invalidate every link you have
  already handed out. Generate a new one when you are ready to reissue. A key
  longer than the method needs is still accepted — Xray derives it down, and that
  has always worked.

- **An AmneziaWG tunnel with transport padding now sets an MTU that fits.** The
  kernel module fixes the interface MTU at 1420 when the device is created — a
  budget computed for plain WireGuard — and never revises it, while the S4
  parameter is added to every data packet afterwards. Turning on **header
  protection forces S4 to at least 12**, which put every such tunnel over a
  1500-byte path by construction. It connected, small packets were fine, and
  anything large stalled: the failure nobody diagnoses.
  **What you will notice:** the device and the client config now agree on a
  reduced MTU (1370 for S4=50, for instance), and large transfers complete. An
  MTU you set yourself is left alone. If you have an AmneziaWG inbound with
  header protection or S4, hand your clients a fresh config — the old one has
  the wrong MTU on it.

- **A WireGuard or AmneziaWG exit can now complete its handshake.** Every exit
  the panel dials was given a new source port every ten seconds, so the far end's
  handshake reply always arrived at the socket the device had a moment earlier.
  The tunnel came up in the few seconds after it was created and then never
  again, which made a correctly configured exit look permanently dead — no
  latency, no real delay, no HTTP.
  **What you will notice:** exits connect and stay connected, and the Test
  buttons return real numbers. Nothing to reconfigure; existing exits start
  working on upgrade.

- **Importing a WireGuard link no longer invents a tunnel address.** `address`
  on a WireGuard outbound is the tunnel interface's own local address. A link
  that did not carry one was imported with `0.0.0.0/0` — which is an *allowedIPs*
  value and means nothing as a local address, so the outbound could not carry
  traffic. The panel's own browser-side importer had the opposite half of the
  same bug: it wrote an empty list, which stops Xray falling back to the address
  it would otherwise choose.
  **What you will notice:** a WireGuard outbound imported from a link that omits
  the address now works. If you have one that never did, delete and re-import it.

- **A Trojan client no longer carries an XTLS flow.** Xray removed the feature
  for Trojan and now refuses a config that contains one — and it refuses the
  *whole* config, so a single such account stopped every inbound on the box from
  starting. The panel emitted it into both the generated config and the share
  link.
  **What you will notice:** nothing, if your Trojan clients were created in this
  panel — saving one has stripped the field for some time. If you imported or
  restored an older configuration, this is the difference between the panel
  starting and not. A Trojan link that used to carry `&flow=` loses it, which is
  correct: no current client can use it either.

- **A fallback path without a leading slash is refused when you save it.**
  Xray rejects one outright, and that rejection fails the *whole* configuration
  — every inbound of every protocol, not just the one with the fallback. The
  Fallbacks card itself wrote such a value: for a gRPC child it filled the path
  in with the bare service name. Two clicks, and the panel would not bring Xray
  back up after a reboot.
  **What you will notice:** the form now fills in `/GunService` rather than
  `GunService`, and a path typed without a slash is refused with a reason.
  **If you already saved one**, it is still stored — this guard stops new ones,
  it does not clean old ones. A restart from the panel is safe (the pre-flight
  added earlier this release refuses to stop a working Xray for a config that
  will not load), but a reboot is not. Open the Fallbacks card on any inbound
  that has one and re-save it.

- **A routing rule that matches on source address now works on a WireGuard or
  AmneziaWG inbound.** Traffic from those inbounds was source-translated on its
  way to the router, so by the time the rule was evaluated every packet carried
  the panel's own internal gateway address instead of the client's tunnel
  address. The rule form offered the field, the rule saved without complaint, and
  it matched nothing. Traffic that leaves through a routing front is no longer
  translated — it does not need to be, because it is re-originated on the way out.
  **What you will notice:** if you wrote a "from 10.x.y.z" rule on one of these
  inbounds and gave up on it, it will start matching. Nothing else changes: an
  inbound with no routing rule is translated exactly as before.

- **A typo in the panel egress proxy no longer disables checks on your outbound
  subscriptions.** When that setting cannot be parsed the panel falls back to a
  direct connection — which is intended — but the subscription fetcher then lost
  two things it had been given: the "allow insecure" tick on the subscription
  itself, and the dialer that refuses a hostname resolving to a private address.
  One mistyped setting quietly turned off a check on a different one.
  **What you will notice:** nothing, unless your egress proxy setting is invalid.
  If it is, the log has been saying so all along:

  ```
  journalctl -u p-ui | grep "Invalid panel egress proxy"
  ```

- **An MTProto inbound with two clients on one email now starts.** The sidecar's
  config keys its secrets by client email, and two clients sharing one wrote the
  same key twice — which the config parser refuses, so the process exited at
  startup and the inbound served nobody. Editing a client had always worked,
  because that path collapses the pair silently, so the inbound only died at the
  next restart.
  **What you will notice:** the inbound comes up with one of the two clients
  served — the same one the live edit path was already keeping — and the log
  names the duplicate:

  ```
  journalctl -u p-ui | grep "share the name"
  ```

- **Stopping the panel now really stops the sidecars it supervises.** The
  supervision job runs every ten seconds and starts anything it finds stopped.
  Shutdown stopped the cores first and the scheduler second — and stopping a
  scheduler only prevents new runs, so a tick already underway brought the
  sidecars back after the panel had let go of them. They kept serving with
  nothing supervising them, and the next start found their ports taken.
  **What you will notice:** MTProto sidecars exit with the panel. If you have
  been killing leftover `mtg-proxy` processes by hand after a restart, you can
  stop.

- **A message with no sender no longer crashes the panel.** Telegram omits the
  sender on a message posted to a channel. Every command handler read it anyway,
  inside a goroutine with no recovery — so the dereference took down the whole
  process, not just the bot, and the same message was redelivered on the next
  poll, which made it a restart loop rather than one crash.
  **What you will notice:** nothing, unless your bot is in a channel — in which
  case the panel stops falling over. Senderless messages are ignored.

- **Importing a share link no longer strips its XHTTP settings and its
  certificate rules.** The panel writes every client-side XHTTP field into a
  link it generates, and its own importer read four of them from a `vless://`
  link's query and three from a `vmess://` one. A `vmess://` import lost the
  padding, session, sequence and uplink knobs, the XMUX block, the split
  download endpoint, and `ech` / `pcs` / `vcn`; a `vless://` import lost `vcn`.
  Since xray-core removed `allowInsecure` in favour of `pcs` and `vcn`, losing
  `vcn` does not weaken the outbound — it stops it connecting.
  **What you will notice:** an outbound imported from a link now matches the
  link. Re-import any outbound you created from a `vmess://` XHTTP link; the one
  you have is missing settings the link carried.

- **An MTProto inbound no longer puts a dead profile in a JSON subscription.**
  The JSON format is an Xray client config, and Xray has no MTProto outbound to
  write — so the profile it produced carried the inbound's name, a routing rule
  pointing at a `proxy` outbound that was not in the file, and nothing else but
  the defaults. A client importing it saw a server in its list and sent every
  byte direct.
  **What you will notice:** MTProto clients are served by the raw subscription
  and the `tg://proxy` link, as they always were. If a subscription contained
  only MTProto inbounds, its JSON endpoint is now empty rather than misleading.

- **A REALITY inbound with a pinned SNI now hands that SNI out.** When you name
  one `serverName` under the REALITY settings rather than leaving the
  `serverNames` list to speak, the panel's own link honoured it and all three
  subscription formats did not — raw and JSON picked a random member of the list
  on every fetch, Clash took the first. So the panel displayed one name and the
  subscription handed out another, and a REALITY handshake offering the wrong
  `serverName` is refused by the server.
  **What you will notice:** if you pinned a `serverName`, every format now emits
  it. If you did not, nothing changes — the list is still used exactly as before.

- **Hosts now apply to WireGuard and MTProto links.** A Host row was stored,
  listed in the panel, and honoured by the JSON and Clash formats — but the
  `wireguard://` and `tg://proxy` links kept the server's own address and port
  regardless. There was nothing to tell you: the Hosts tab looked configured and
  the link quietly pointed somewhere else.
  **What you will notice:** these inbounds now render one link per enabled Host,
  the same as every other protocol. If you added Hosts to a WireGuard or MTProto
  inbound, the links your clients hold are wrong until they re-import.

- **A Host's SNI, fingerprint and ALPN now reach a Hysteria link.** The Hosts
  page offers all three per host; the Hysteria link carried only the certificate
  pins and the insecure flag and ignored the rest.
  **What you will notice:** a Host that fills those fields changes the link. A
  Host that leaves them blank renders exactly what it rendered before.

- **Adding a Host to a Hysteria2 inbound no longer switches off port hopping.**
  The `mport` range was written only on the path taken when an inbound has no
  Hosts, so turning on a single Host row silently dropped it for every client of
  that inbound — while the panel went on showing the range.
  **What you will notice:** port hopping keeps working with Hosts configured.

- **The AmneziaWG config the panel hands out now carries every obfuscation
  setting.** The downloadable `.conf` left out H1-H4 and all six timer ranges —
  while the share link beside it, the subscription and the server itself all had
  them. A client importing that file used WireGuard's default header values
  against a device configured with custom ones, and a mismatched pair does not
  connect slowly, it never connects at all.
  **What you will notice:** the downloaded config matches the link and the
  server. If you have been handing out `.conf` files for an AmneziaWG inbound
  with custom headers or timers, re-download them.

- **A WireGuard share link now carries its DNS, pre-shared key and keepalive.**
  The subscription page rebuilds a config from the link rather than from the
  inbound, and it reads exactly those three keys — which the link never wrote.
  So a config rebuilt that way silently fell back to `1.1.1.1`, no pre-shared
  key and no keepalive, and a peer whose server requires a pre-shared key cannot
  connect without it.
  **What you will notice:** links are slightly longer, and a config built from
  one describes the same tunnel as the config downloaded directly.

- **A subscription server that cannot start no longer takes the panel with it.**
  If its port was already in use, the whole process exited — after the panel's
  own interface was already running. So the one page you would use to change
  that port went down with it, on every boot, and the only way out was editing
  the database by hand.
  **What you will notice:** the panel stays up and logs the reason. Subscription
  links are not served until you fix the port, and the log line says so:

  ```
  journalctl -u p-ui | grep SUB_SERVER
  ```

- **A wrong Listen Domain can be undone from the command line.** Setting
  `webDomain` to something that does not match how you actually reach the panel
  makes every request return 403 after a restart — including the settings page
  that would fix it. There was no flag for it.
  **If you are locked out**, from a root shell on the server:

  ```
  p-ui setting -webDomain -
  ```

  That clears it and the panel answers on any host again. Pass a domain instead
  of `-` to set one.

- **An AmneziaWG inbound the kernel would reject is refused when you save it.**
  The obfuscation settings were only checked when the device was created — and
  the kernel module reports a bad combination with an error code and a log line
  you will never see. So the panel saved the inbound, said it worked, and the
  tunnel quietly did not come up, while the config handed to clients described
  the settings you asked for.
  **What you will notice:** an impossible combination (a junk range with its
  bounds the wrong way round, two headers covering the same value, a
  header-protection key of the wrong length) is refused at the save, naming what
  is wrong. Inbounds with no obfuscation block are unaffected, and no other
  protocol gains a new refusal.

- **A failed restore now tells you what actually failed.** If `pg_restore`
  rejected your dump, the panel reported that it could not reopen the database
  — a different problem with a different fix. **What you will notice:** the
  restore error names the real cause, and if both go wrong the message carries
  both. A restore that succeeds but leaves the database unreachable now says so
  instead of reporting success.

- **Restoring a database no longer risks crashing the panel.** The connection
  handle was swapped while eighteen background jobs were using it, with no
  synchronisation — and a reopen that failed replaced a working connection with
  nothing, turning every page and job into a crash. **What you will notice:** a
  restore is safe to run on a busy panel, and a reopen that fails leaves you
  with errors you can read rather than a panel that falls over.

- **A client that runs out of quota stays cut off.** The depletion pass rewrote
  the inbound's stored settings without taking the lock every other writer
  takes, so a client being added at the same moment could restore the depleted
  client's `enable: true`. That did not heal: the panel only re-checks clients
  it still believes are enabled, and this one had already been marked disabled
  elsewhere — so no later pass noticed, and an exhausted quota was served until
  someone reset that client's traffic by hand.
  **What you will notice:** depletions hold. The settings update now happens
  just after the traffic save rather than during it, so a client added at the
  same time keeps its place and the depleted one still gets cut off. A client
  you reset in that same instant is left alone.

- **Deleting an inbound removes its stored credentials and fallbacks.** They
  were left behind, and the panel reuses inbound ids — so a new inbound created
  later could inherit a deleted one's credentials and fallback entries.
  **What you will notice:** the rows go with the inbound. Anything already
  orphaned by an earlier delete is cleared once, at the next startup, before any
  id can be handed out again.

- **The routing-rules migration runs once, not on every restart.** The one-time
  conversion that turns template routing rules into panel rules had no record
  that it had already run, so it re-consumed the template on every boot. If you
  edited your Xray template afterwards, those edits were converted away and
  removed — again, every time the panel came up.
  **What you will notice:** your template stays as you left it. Panels that
  already converted are unaffected; the marker is written the first time this
  version starts.

- **Back Up and Import DB work on a database that needs TLS options.** They
  passed only `sslmode` to `pg_dump` / `pg_restore` and dropped everything else
  in your connection string — so a managed PostgreSQL needing a CA bundle
  (`sslrootcert`), a client certificate, a connect timeout or a session
  attribute failed to back up on a panel that was otherwise connected and
  healthy. The scheduled Telegram backup failed the same way.
  **What you will notice:** backup and restore now use the same connection
  parameters the panel itself connects with.

- **Traffic that fails to save is now reported as a failure.** The traffic
  writer committed inside a deferred function and only logged a commit error —
  it could not return one, so a poll whose transaction rolled back was reported
  as a success. The counters had already been zeroed in the core, so those bytes
  were gone: silently unbilled, with one warning line as the only trace.
  **What you will notice:** nothing, on a healthy panel. On a struggling
  database the traffic job now logs a real error instead of quietly losing a
  poll. The same defect was fixed in the outbound traffic writer alongside it.

- **Resetting an outbound's counters sticks.** Outbound traffic was accumulated
  by reading the row, adding in memory and writing the total back. A reset that
  landed between those two steps was erased — the poll wrote back a total it had
  computed moments earlier, and the cleared counters reappeared with the new
  traffic added on top. Measured: a reset of 1000 bytes came back as 1008.
  **What you will notice:** a reset holds, even under a busy poll. Every other
  traffic counter in the panel already worked this way.

- **A renewed MTProto client's quota is cleared after the save, not during it.**
  The call that clears the sidecar's own quota counter ran inside the open
  traffic transaction rather than after it — a network round trip to the
  sidecar holding a database transaction open, on every renewal. It also ran
  even when the save then failed.
  **What you will notice:** less database contention during renewals, and a
  renewal the database rejected no longer un-blocks the client in the sidecar.

- **Quota and expiry now actually cut off MTProto and WireGuard clients.** When
  a client or inbound ran out of quota or reached its expiry date, the panel
  disabled the row and then told **Xray** to drop them — whichever core was
  really serving them. For an MTProto or kernel-WireGuard inbound Xray has never
  heard of that tag, so the call failed, the panel scheduled a core restart, and
  **the client stayed connected**. Every poll repeated it.
  **What you will notice:** a depleted or expired client on those protocols is
  now disconnected, and the restarts that failure was causing stop. The same
  applies in reverse — a renewed client is restored in the core that serves
  them rather than being enabled in the database and left cut off.

- **An inbound can no longer take a tag the panel generates.** Tagging an
  inbound `panel-egress` made the panel skip injecting its own outbound bridge —
  the bridge and its routing rule silently vanished from the generated config,
  behind one warning line in the log. `api`, `direct` and `blocked` were
  claimable the same way.
  **What you will notice:** saving such an inbound is refused and the message
  names what owns the tag. The reserved set is read from your Xray config
  template, so renaming an outbound there moves the reservation with it rather
  than leaving the old name locked and the new one free.

- **In a Telegram group, only an admin can fill in a client being added.** The
  add-client form remembers which *chat* it is waiting on, not which person — so
  in a group chat any member's next message was written into the admin's
  in-progress client as its email, Telegram id or comment.
  **What you will notice:** messages from non-admins are ignored while a form is
  open, as they already were for the contact-sharing step.

- **A per-client IP limit now applies wherever that client connects.** The cap
  belongs to the client, but enforcement was decided by whichever of their
  inbounds happened to have the lowest id. Disable that one while the same
  client keeps connecting through another, and their cap silently stopped
  applying — no ban, no record, no warning.
  **What you will notice:** a client attached to several inbounds is now capped
  as long as *any* of them is enabled. If you relied on disabling one inbound to
  lift someone's cap, that no longer works — clear the limit on the client
  instead.

- **The login attempt limit now holds when attempts arrive at once.** The
  five-failure cap checked your budget, released its lock while the password was
  verified, and only counted the failure afterwards — so requests sent
  simultaneously all passed the check together. Measured: **50 of 50 concurrent
  attempts admitted against a cap of 5.**
  **What you will notice:** nothing, unless someone was doing this to you. The
  cap behaves the same for ordinary use.

- **A forwarded client address can no longer be forged.** Behind a reverse proxy
  the panel read the *first* entry of `X-Forwarded-For`. That header is
  append-only — each hop adds what it saw — so the first entry is whatever the
  caller sent, and the real address is on the *right*. The login limiter keys on
  this value, so a fresh, unblocked identity was one header away.
  **What you will notice:** the address in your login logs and Telegram alerts
  is now the nearest one your proxies did not vouch for. If you run a chain of
  proxies, list all of them in **Trusted proxy CIDRs** so the panel can tell
  yours from a client's.

- **`Trusted proxy CIDRs` now means what it says on the subscription server.**
  It was ignored at two values — empty, and the shipped default — and at either
  one the server accepted `X-Forwarded-Host` from **any address on the
  internet**, then built subscription URLs from it.
  **What you will notice:** most people, nothing. The shipped default is
  `127.0.0.1/32,::1/128`, which is exactly the same-box reverse proxy nearly
  every deployment runs, and that keeps working. **If your reverse proxy is on a
  different machine**, add its address to that setting or your subscription
  links will use the panel's own host instead. The panel already logs a warning
  naming the address it ignored:

  ```
  journalctl -u p-ui | grep trustedProxyCIDRs
  ```

- **Two-factor authentication with no secret no longer lets anyone in.** If 2FA
  was switched on while the secret was empty, the panel compared your code
  against one computed from an empty key — a six-digit number anybody could work
  out from the clock alone. **What you will notice:** that state now refuses
  every login instead of accepting a public constant, and enabling 2FA without a
  secret is refused at the settings page. Nothing else about how a code is
  checked has changed.
  **If you are locked out**, from a root shell on the server:

  ```
  p-ui setting -resetTwoFactor
  ```

- **LDAP no longer signs people in to the panel unless you say who may.**
  `ldapEnable` is the switch for *syncing clients* from your directory. It was
  also, silently, the switch for signing in to the panel — and the check behind
  it only proved the person exists in the directory and knows their own
  password. With the default user filter of `(objectClass=person)`, that is
  everyone.
  **What you will notice:** panel sign-in through LDAP is now off unless you set
  a filter saying which accounts may administer this panel. **Client sync is
  completely unaffected**, and your panel password still works — LDAP is only
  consulted after the local password fails, so nobody is locked out. To turn it
  on, from a root shell:

  ```
  p-ui setting -ldapAdminFilter "(memberOf=cn=panel-admins,ou=groups,dc=example,dc=com)"
  ```

  Pass `-` to turn it off again. It is deliberately not on the Settings page:
  granting panel administration is a decision to make from the server, and
  keeping it out of the settings form means a settings save can never clear it.

- **The panel says at startup when an authentication setting cannot work.**
  If 2FA is on with no secret, or LDAP is on with no admin filter, a line is
  logged at **Error** on every start. Nothing is changed for
  you — turning a security setting off on your behalf is worse than leaving it
  misconfigured. **To check your own box:**

  ```
  journalctl -u p-ui | grep AUTH_POSTURE
  ```

- **A SOCKS/mixed inbound can no longer show credentials it is not enforcing.**
  Xray treats `auth: "password"` as “check the accounts” and **every other
  value, including no value at all**, as “accept everyone” — while still
  parsing the accounts and showing them in the panel. An inbound saved that way
  was an open proxy on a public port that looked password-protected. The panel's
  own form always sets the field, so this could only arrive through the REST
  API, through Import, or by hand-editing the JSON in the advanced editor.
  **What you will notice:** saving such an inbound is now refused, and the
  refusal says which of the two you meant. Editing an affected inbound — even to
  change something unrelated — refuses until you fix it, so the state cannot
  survive being touched. HTTP inbounds are unaffected: they have no such switch
  and always enforce their accounts.

- **The panel now reports an open proxy it finds at startup, on every start.**
  Nothing is disabled and nothing is rewritten — the settings are yours — but a
  line is logged at **Error** level for each affected inbound. **To check your
  own box:**

  ```
  journalctl -u p-ui | grep OPEN_PROXY
  ```

  Each line is `[OPEN_PROXY] inbound id=<id> tag="<tag>" port=<port>:` followed
  by the count of accounts and the auth value found. Open the inbound the id
  names, set its auth to `password`, and save — or remove the accounts if it was
  meant to be open. No output means no affected inbound.

- **Removing a tunnel now removes its NAT rules on Ubuntu 22.04 and Debian 12.**
  The teardown used the nftables `destroy` command, which those releases' nft
  has never heard of — measured as a parse error, not a runtime failure — so
  the panel's masquerade table stayed on the box with its rules still
  translating after the last tunnel was gone. Newer releases were unaffected.
  **What you will notice:** on those two releases the table is now actually
  removed. Nothing changes on 24.04 or 26.04.

- **The IP Limit field now tells you when it would do nothing.** The panel
  checked `fail2ban-client -h` to decide whether a per-client IP limit could be
  enforced — a question the client answers out of its own argument parser, with
  the daemon stopped and with p-ui's jail deleted. Measured on a real box: with
  the jail removed (which this menu's own “Only remove IP Limit
  configurations” does), `-h` still exits 0 while asking about the jail exits
  255. So the field stayed green, the log said addresses were over their cap,
  and no packet was ever dropped.
  **What you will notice:** a third state. If fail2ban is installed but p-ui's
  jail is not loaded, the IP Limit control is disabled and says so, with what to
  re-run. **On upgrade this can look like a regression — it is not.** A box
  showing that message was never enforcing; it just had no way to tell you.

- **The fail2ban menu stops deleting configuration it does not own.**
  “Uninstall Fail2ban and IP Limit” ran `rm -rf /etc/fail2ban`, taking every
  jail, filter and `.local` override on the box — while the option above it
  already removed p-ui's three files surgically. Separately, the cleanup that
  runs on update deleted from `[p-ui-ipl]` to the next blank line, and a jail
  file whose next section follows immediately has none: it ran to end of file
  and took the operator's `[sshd]` and `[recidive]` jails with it.
  **What you will notice:** both paths now remove exactly p-ui's own section
  and files. The uninstall leaves the rest to apt.

- **Fail2ban installs entirely from your distribution.** The Ubuntu path ran
  `pip install pyasynchat --break-system-packages` — the only step in the whole
  install that left the distro archive, and it fails wherever PyPI is
  unreachable. It worked around a bug fixed in Ubuntu's own package in June
  2024; the shipped package carries its own replacement module and depends on
  the other one. The pip copies were also installed where they take precedence
  over the packaged ones.
  **What you will notice:** the install works on networks that cannot reach
  PyPI, and stops shadowing an apt-managed dependency.

- **The menu reads the panel's settings, and the real SSH port.** `p-ui.sh`
  never loaded `/etc/default/p-ui`, so from an ordinary login shell “Change Ban
  Duration” rewrote the ban rules with no database connection — dropping the
  panel port from the exemption list — and pointed the jail at
  `/var/log/p-ui` even when the panel logs elsewhere, leaving it watching a file
  nobody writes. The SSH exemption was scraped from `sshd_config` alone, missing
  the `Include sshd_config.d/*.conf` every cloud image ships, so SSH on 2222
  exempted port 22.
  **What you will notice:** the ban rules exempt the ports that are really in
  use — including the port your current session is on, whatever the config
  says — and the jail watches the file the panel actually writes.

- **QR codes and “Export links” now show your hosts.** The panel built those
  links in the browser, and the browser has no concept of a Host row — so on a
  CDN-fronted inbound the QR you scanned and the links you exported named the
  origin address and port, from the same page that lists the hosts. Where the
  origin is firewalled to CDN ranges that link cannot connect at all; everywhere
  else it publishes the address the hosts existed to hide. The Inbounds page
  contradicted itself: “Export all links” (rendered on the server) and “Export
  links” on a single inbound (rendered in the browser) disagreed about the same
  inbound.
  **What you will notice:** both now come from the server, so they match the
  subscription and each other. If the panel cannot reach that renderer it says
  so and shows nothing, rather than quietly falling back to the origin link —
  a wrong address that looks exactly like a right one is the failure this
  change exists to remove. Kernel WireGuard peers are unaffected either way:
  they are not host-fronted.

- **`GET /panel/api/clients/links/:email` now returns the client's host links.**
  It rendered straight from the inbound, with no host lookup at all, so on a
  CDN-fronted inbound it handed back the origin address and port — the one the
  hosts exist to replace, and on an origin firewalled to CDN ranges the one
  address that cannot connect. The subscription endpoint rendered the same
  client correctly at the same moment.
  **What you will notice:** the endpoint returns one link per enabled host, in
  host order, matching what a subscriber gets. An inbound with no hosts returns
  exactly what it returned before.

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
  was, with an error. And the new binary is asked whether it can load your
  configuration *before* it is installed at all, so a version that cannot is
  never put in place — rather than landing and then refusing every restart
  afterwards with nothing to put the old one back.

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

- **A client created with its switch off is now genuinely stored off.** This was
  reported as fixed earlier in this release and it was not. That fix was real
  — the database was supplying `true` for a boolean the INSERT had dropped —
  but a second mechanism sat above it: three creation paths carried an explicit
  “if it is off, turn it on” that overrode the caller before the database ever
  saw the value.
  It overrode three deliberate things: the enable switch on the client form, an
  import of a client that was **disabled when you exported it**, and the
  Telegram bot's own “add disabled” button. Nothing depended on it — every
  caller already set the value, including the LDAP sync.
  **What you will notice:** staging a client disabled works everywhere now, and
  importing a backup no longer switches your disabled clients back on. **Worth
  checking by hand:** a client created or imported disabled before this fix was
  stored enabled and could connect. There is no record of what was intended, so
  the panel cannot find them for you.

- **A certificate that fails to load no longer degrades the panel silently.**
  Both the panel and the subscription server bind their socket before deciding
  on TLS, so when the key pair failed to load they kept serving — in cleartext,
  on the very port HTTPS was configured on — with one generic log line. Session
  cookies stop being Secure, HSTS is not sent, and on the panel node mTLS client
  certificate verification is never configured. A truncated certbot renewal is
  enough to cause it, and the panel reports itself as up, because it is.
  **What you will notice:** the failure is now one greppable line naming the
  files, the load error, the address and everything that is off:
  `journalctl -u p-ui | grep TLS_POSTURE`. It still starts — refusing to boot
  would lock you out of the settings page that fixes it, which is the trap the
  earlier lockout fixes exist to avoid.

- **The CSRF token you were given before signing in is replaced when you sign
  in.** A token is handed to anonymous visitors so the login form can post, and
  it used to be carried into the authenticated session unchanged — so anyone who
  could plant one in your browser beforehand held a valid token for your
  authenticated session afterwards.
  **What you will notice:** nothing. The token is rotated inside the same cookie
  write, and the page reloads after login with a fresh one. Changing your
  username or password rotates it too; if the interface is open in another tab
  at that moment, its next action costs one extra round trip while it refetches.

- **A client, node, subscription or routing rule created with its switch off is
  no longer stored switched on.** *(For clients this entry was incomplete when
  written — a second mechanism overrode the switch above this one. See “A client
  created with its switch off is now genuinely stored off” above.)* A
  zero-valued boolean was dropped from the INSERT whenever its column carried a
  database-side default, so the database supplied `true` instead. The consequences differed by row: a **client** staged
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
