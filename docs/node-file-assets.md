# Node file assets

The master compiler sends node-scoped file material inside each runtime's desired
configuration. A file has a logical ID, a canonical SHA-256 digest and immutable
bytes. Typed configuration fields refer to `node-asset:<id>:<sha256>`; a master
filesystem path never selects a node file. SSH WSS currently uses this contract
for its certificate and private key. Embedded certificates in other adapters
remain part of their own typed configurations.

The agent retains the master's complete deployment and verifies its original
hash. Each worker receives only its own runtime configuration and file bytes,
with a separate local payload hash and unchanged node, control-plane, authority
generation and revision. Other cores' credentials never enter that worker's
private desired journal. Omitted runtimes receive an explicit empty bundle so
they still stop and release obsolete files. Validation across all workers
finishes before the agent starts application; the master acknowledgement remains
bound to its original complete deployment.

The worker resolves references under its actual `--state-dir/assets/<sha256>`.
The Linux asset store checks ownership, permissions and inode identity, refuses
symlink traversal, and accepts only regular, singly linked files. Directories
are mode 0700 and files 0600. Existing bytes or permissions that disagree with
the immutable content are rejected. Publication orders file fsync, atomic
no-replace rename and directory fsync; it never overwrites an active file.

All declared files are validated before staging. The runtime must consume every
declared logical reference, and every referenced file must be supplied. SSH
loads and validates the certificate/key pair before receiving a new durable
desired revision or changing a listener. Validation may create unreferenced
immutable cache files; rejected candidates are cleaned while the active and
durable desired files remain retained.

The durable worker journal keeps the applied and desired configurations with
their original logical references and bytes. Local absolute paths are only an
in-memory rendering. Cleanup retains both configurations until application
commits. A process restart revalidates and replays desired material before
serving. After a partial backend failure, a bounded in-memory set additionally
retains every possibly active file until successful convergence or a confirmed
shutdown. This covers a later apply failing before it reaches the engine.

The worker's successful apply response includes `X-Pui-Assets-Sha256`. The
agent verifies the exact per-runtime manifest, including omitted runtimes, before
acknowledging the aggregate `ApplyResult.asset_manifest_sha256` to the master.
The master verifies that manifest before advancing the applied revision or
accepting associated rate/stop proofs. A manifest is a domain-separated digest
of sorted, length-prefixed runtime ID, logical file ID and content digest claims.
Even a zero-file configuration requires its explicit 32-byte manifest; a missing
receipt cannot prove application. Exact lost-response retries remain idempotent.

Rejection bounds are 256 files per runtime, 8 MiB per file, 16 MiB decoded file
bytes across the bundle, and the existing 32 MiB serialized deployment limit.
At most 512 distinct possibly active file digests are retained in memory; a new
candidate exceeding that uncertainty bound is refused before changing durable
desired state. Cleanup removes at most 256 orphan files per pass and refuses an
unexpected or overfull cache. These limits do not establish fleet capacity.

Qualification includes real Linux TLS 1.3 handshakes with the SSH WSS listener,
custom state directories, certificate rotation, invalid hash/pair/path/material
rejection, durable database reopen and pending replay, partial backend failure,
exact worker headers, PostgreSQL applied receipts, and lost-response replay.
Real worker fan-out tests inspect private journals, materialized files, local
restore and omitted-runtime shutdown; three isolated controls detect leaking the
full bundle, bypassing the master hash or changing the authority generation.
The filesystem suite separately covers symlinks, hard links, ownership, FIFOs,
permissions, concurrent publication and syscall ordering. The integration
removed-guard harness is `tools/nodekernel/asset-integration-negative.py`; run
with the isolated PostgreSQL test DSN configured. No live material or publisher
credentials are used. Updated binaries still require release/systemd integration
qualification; the earlier candidate bundle predates this protocol field.

Current builds use node protocol generation 2 and durable state format 3.
Both sides explicitly negotiate that protocol before deployments can apply.
New journals initialize the current format; unversioned, experimental formats1/2,
corrupt and future format journals are refused without modifying their bytes.
The release verifier and installer accept only signed format3 manifests. A
same-format update may recover its previous executable set while retaining
credentials and accounting; an experimental format1/2 candidate cannot be its
rollback target. No migration or automatic reset is provided for those
unreleased experimental files. The worker checks its state format before opening
the native backend, and native credit journals have their own format marker.
This fresh-node boundary protects pending native authority transitions from old
JSON readers. Actual Linux startup and installer qualification is still required.
