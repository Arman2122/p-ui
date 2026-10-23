# Headless node bootstrap and release activation

These tools are implementation foundations. They are not yet a production-qualified installer: real systemd isolation, all supported distributions and architectures, native daemon provisioning, and publisher operational key ceremonies remain release gates.

The installer itself, its adjacent systemd templates, and the bootstrap release verifier must arrive through a trusted bootstrap channel. A bootstrap directory contains six runtime executables, `penhoon-release`, `manifest.json`, and `manifest.sig`. Its offline Ed25519 publisher signature authenticates the initial executable set before the verifier runs. The independently authenticated TUF root then authorizes the release through fresh repository metadata. Neither the online master nor an enrollment ticket possesses these signing keys.

## Installation

Install Python 3, OpenSSL, systemd, iproute2 and nftables using the supported distribution's package manager. The bootstrap supports Debian 12+ and Ubuntu 22.04, 24.04, and 26.04. Supply the bootstrap publisher public key and TUF root through authenticated channels. Do not download the initial root opportunistically from the repository. Keep the enrollment package private.

```sh
python3 deploy/node/install-node.py --release-dir /path/to/release --trusted-key /path/to/publisher.pem --dry-run
sudo python3 deploy/node/install-node.py --release-dir /path/to/release --trusted-key /path/to/publisher.pem --enrollment /private/enrollment.json --tuf-root /trusted/root.json --metadata-url https://updates.example/metadata --targets-url https://updates.example/targets
```

`--native` additionally enables the native WireGuard/AmneziaWG runtime. Native kernel modules and tools require separate provisioning; the current bootstrap loads WireGuard only. Do not treat this flag as an OpenVPN/ocserv installer.

The installer verifies every executable before activation, pins repository trust, creates private service accounts, enrolls once, and stores a recoverable install transaction. Retrying an interrupted install requires the same bootstrap release, enrollment identity, publisher key, initial TUF root, repository endpoints and runtime selection. Rotated TUF roots are retained across retries; the bootstrap root never resets an existing trust cache. An enrolled vault is retained and its identity checked before reuse. Subprocess environments exclude panel database credentials. Dry-run validates only the bootstrap bundle and performs no repository fetch or installation.

## Updates and rollback

```sh
sudo python3 deploy/node/install-node.py --update --sequence 42
sudo python3 deploy/node/install-node.py --recover
sudo python3 deploy/node/install-node.py --rollback --sequence 43
```

Updates use the installed TUF verifier and protected metadata cache. The pinned `github.com/theupdateframework/go-tuf/v2` client verifies root signature thresholds and sequential key rotation, timestamp/snapshot freshness and rollback checks, and target delegations. Metadata must be current to authorize every new activation; unavailable or expired metadata is an error. HTTP redirects are refused. A missing trust cache is not silently rebuilt from the bootstrap root.

A release sequence cannot decrease or identify different content once accepted. The high-water mark is committed before candidate execution and retained after failed activation. A retry may reuse the exact accepted manifest after fresh TUF authorization. This generation accepts signed `stateFormat: 3` only. Experimental formats1/2, unversioned journals and unknown future formats are refused without resetting state or re-enrolling. Format3 adds native pending-authority storage; it requires fresh nodes. No migration is provided for unreleased experimental candidates; a future storage migration requires its own qualified workflow.

Workers validate their format3 state journal before opening a backend or its credit journal. Format2 binaries therefore refuse these nodes before native recovery or kernel changes. Native credit journals also carry an explicit format3 marker. Update, rollback and interrupted-activation recovery cannot cross the format boundary; existing files remain intact. Wire protocol generation remains 2. Linux startup, installer and native authority traffic qualification must cover this generation before release.

Activation stops the agent and runtime units, atomically changes the executable directory, starts the runtimes, checks their local control readiness, and starts the agent. Every unit is checked individually: systemd's multi-unit `is-active` succeeds when just one unit is active. This currently interrupts client sessions across the runtime set. Control readiness does not prove every listener or external destination is healthy.

An unsuccessful activation attempts to restore the exact previous committed executable set. If rollback or the updater itself fails, `--recover` replays that fallback from the durable journal before another update. Recovery never restores a database backup, re-enrolls the node, recreates a quota journal, or lowers the accepted publisher sequence. If the disk cannot persist recovery state, the journal remains pending and the error is returned.

An intentional rollback is a newly signed release with a current sequence and executable hashes matching the previous committed release. Passing an older signed package alone does not authorize a downgrade. Binary compatibility is a publisher assertion through `stateFormat`; it must be justified by compatibility tests before signing.

Fleet update scheduling, canaries, master-visible operation status, publisher repository deployment, root-key ceremonies and operational recovery drills remain unfinished. These limitations must be resolved before exposing automatic remote updates.

## Repository contract

The repository uses TUF consistent snapshots and SHA-256 target names. Each release has `nodes/PLATFORM/SEQUENCE/manifest.json` and seven executable targets under the same prefix. A platform delegation can use `nodes/linux-amd64/*/*`; a single trailing `*` does not match both path levels. The manifest's platform, sequence, state format, complete asset names, lengths and hashes are themselves signed through the TUF target metadata. Every executable, including the verifier, must also be an authorized delegated target with the exact matching length and SHA-256. Downloads stream to a private staging directory; no archive extraction or unverified execution is involved.

Root and role signatures are implemented by maintained upstream TUF code. The application adds its platform/state compatibility policy, private cache permissions, process locking, metadata fsync, immutable activation staging and bounded streaming artifact verification. Already accepted local releases retain a root-owned digest receipt for transaction recovery; their bytes are rechecked without requiring old keys or metadata to remain current. This permits offline rollback of an interrupted activation without permitting an offline new update.

## Validation

Build the runtimes and verifier with `bash tools/noderelease/build.sh /absolute/output linux-amd64` (or another listed platform). The nested sing-box/MTProto builds use their checksum-pinned maintained patches; building MTProto requires authorized access to its private pinned dependency. The build helper does not sign or publish. On an approved offline signing environment, `go run ./tools/noderelease sign --key /private/publisher.key --dir /absolute/output --release VERSION --platform linux-amd64 --sequence SEQUENCE --state-format 3` creates the bootstrap signature. Production releases also need TUF target metadata from the publisher's reviewed repository tooling. Never reuse a sequence for different content; republishing previous executable hashes for rollback requires a new sequence.

Go tests use a real TLS repository and real threshold/delegation signatures to reject expired or rolled-back metadata, incomplete root rotation, unauthorized platform paths, mismatched manifests/artifacts and redirects. They reopen the persisted trust cache after rotation and rollback attempts.

`tools/nodekernel/install-test.sh` verifies bootstrap signatures and rejects modified assets. `install-activation-test.sh` runs inside a private mount namespace, masks installation directories, and mocks account/service commands and the release-fetch helper. It exercises interrupted enrollment/activation, unavailable metadata, readiness failure, rollback failure, commit-write failure, abrupt updater death, incompatible state formats, unprotected installed assets, and downgrade rejection while retaining credentials/accounting fixtures. `install-negative.sh` proves the suite fails when sequence enforcement, recovery of an uncertain commit, or individual service readiness checks are removed. These tests do not create host accounts or start host services; they do not substitute for a real systemd VM qualification.

The [disposable VM harness](../../tools/nodekernel/vm/README.md) runs real systemd installation and update tests using synthetic enrollment/TUF services and actual runtime binaries. Its native-disabled scope and evidence requirements are documented separately.

The [shared-kernel namespace harness](../../tools/nodekernel/namespace/README.md) has passed the real systemd service/installer gate with native disabled. It does not replace the separate VM, bare-metal, native-runtime or distribution-matrix gates.
