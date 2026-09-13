# Core updates

Xray, MTProto and sing-box implement the same version-management interface.
Installation replaces the executable on disk after validation; it does not
restart running instances. The version dialog distinguishes successful
installation from a failed version refresh and offers a separate managed restart.
Loaded processes whose executable was replaced retain a restart-required notice.

## Compatible sing-box channel

The panel uses releases tagged `singbox-vX.Y.Z` in `Arman2122/p-ui`. Each platform
requires both `sing-box-X.Y.Z-linux-PLATFORM.tar.gz` and its `.sha256` sidecar.
Drafts, prereleases, incomplete asset pairs and versions outside the adapter's
supported minor line are not offered. The current line is 1.14, with its minimum
defined by `internal/cores/internal/singbox/version.go`.

Standard upstream binaries omit the statistics API used for per-user billing.
Build the compatible variant from unmodified upstream source with
`tools/build-singbox-release.sh`, or run the **Compatible sing-box release**
workflow with a stable upstream tag. Required build tags come from the updater's
`RequiredBuildTags` constant. The workflow produces all seven supported Linux
platforms and defaults to a draft. Publishing uses `latest=false` because a core
release must not become the panel's latest release.

Before replacement, the updater verifies the archive SHA-256, exact executable
version and required build features. It checks TUIC and AnyTLS with accounting
and control enabled, then validates every managed sing-box instance's current
configuration. Validation failure, cancellation or a concurrent install leaves
the existing executable intact. Installs are serialized, and the final rename
is atomic within the executable's filesystem.

The release build's native architecture runs `TestPublishedUpdateCandidate`.
For a full isolated upgrade rehearsal, set `SINGBOX_UPDATE_E2E_ARCHIVE` and
`SINGBOX_UPDATE_E2E_VERSION` and run `TestPublishedUpdateInstallation` inside a
private network/mount namespace. That test installs the actual archive, checks
real traffic/session accounting, preserves an active process during staging,
and applies the replacement only after an explicit restart.

## Other engines

Xray verifies published digests, exact executable identity/version and its
current configuration. Its installed-version report reads the file on disk.
MTProto uses the panel's fork, enforces the minimum supported version, rejects
draft/development releases and verifies that the downloaded version matches the
requested stable tag. It does not claim a separate configuration dry-run check.

SSH updates with the panel. System package and kernel cores retain their own
maintenance paths. Core install/restart controls never adopt or restart unrelated
host processes.
