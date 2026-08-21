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

- **An inbound whose TLS certificate file does not exist yet still saves.**
  Saving an inbound that points at a path certbot has not written is the ordinary
  order of operations and is no longer refused. The certificate's *shape* is still
  checked — an empty one is still rejected — and the core is never swapped for a
  config it cannot load, so a file that never appears cannot take the panel down.

### Security

- A Telegram user can no longer read another client's subscription URL, config
  URIs or QR codes by sending a crafted callback naming their email. Client link
  callbacks now resolve ownership against the caller's Telegram id.

- A fresh install no longer leaves `/usr/local/p-ui` owned by the release
  archive's build uid. Existing installs are repaired by the next run of the
  install or update script.
