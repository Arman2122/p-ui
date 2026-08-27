# Changelog

Operator-visible changes only — things that behave differently on a panel you
upgrade in place. Bug fixes that simply make a broken thing work are not listed
here; the git log is the full record.

## Unreleased

### Behaviour changes

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
