# Governed Xray build

The root module pins Xray to `v1.260327.1-0.20260908222543-52a412d9e2f5`. `patches/concurrency.json` binds every modified upstream file to its SHA-256 and exact replacement context. The saved implementation requires Xray patch marker **7** and the authentication patch marker from `sing-shadowsocks v0.2.9`; unpatched runtime builds fail compilation.

`tools/nodecore -core xray` verifies and copies Xray and its declared authentication dependency into the workspace cache, then creates temporary module replacements for those copies. Dependency module versions, file checksums and replacement contexts must match the reviewed manifest. The tool never edits the module cache or repository module manifests. Existing `GOFLAGS` and source overlays are preserved; the temporary module flag is quoted using Go's field rules and inherited by child Go commands. This matters for integration tests that build a separate agent or runtime process.

```sh
go run ./tools/nodecore -core xray -race
go run ./tools/nodecore -core xray -go test -- -race ./internal/controlplane
go run ./tools/nodecore -core xray -lint -- ./internal/cores/noderuntime/xray/...
go run ./tools/nodecore -core xray -build /absolute/path/penhoon-xray
```

`-prepare-only` prints an absolute `runtime.mod` path and retains its directory. Patched Xray lives in sibling `source/`, and the patched dependency lives in `dependency-0/`; this mode supports explicit isolated negative controls. Normal commands remove their own temporary directory after completion. `Makefile`, CI, the panel release build, and the seven-binary node release builder use this path.

Go test modes default to `-trimpath` through their process-local `GOFLAGS`, including child Go builds. Explicit command-line or environment `trimpath` choices retain their normal precedence. This removes temporary replacement paths from compiled dependency cache identities; it does not establish whole-executable reproducibility.

Lint and vulnerability tools use an ephemeral workspace with the same verified Xray and dependency replacements. Their Go-version probe disables module mode, which is incompatible with an inherited module-file flag. Other Go flags and overlays remain intact; analysis with a separate user-supplied module file is explicitly refused rather than silently changing its meaning.

An existing Go workspace is also refused. Select `GOWORK=off` explicitly when a standalone repository build is intended; the wrapper never changes the user's workspace configuration.

For cross builds, compile `tools/nodecore` for `GOHOSTOS`/`GOHOSTARCH` first, then run that host executable while retaining the desired target environment. The release scripts do this automatically.

The temporary replacement directory currently appears in Go build metadata, so this build path has not yet qualified byte-for-byte reproducibility. Qualification must record the exact built artifact hashes before signing. These local patches need upstream tracking and checksum review when the pin changes.

Patch marker 7 covers **twelve Xray source files**, plus one separately pinned `sing-shadowsocks` source file:

- The packet bridge synchronizes cached buffers during close, and Hysteria synchronizes connection and manager shutdown state.
- The stream bridge in `common/singbridge/pipe.go` interrupts the raw transport and joins copy tasks before releasing buffered protocol state. It propagates link closure while preserving half-close behavior.
- Vision retains pointer values, validates the reflected buffer layout, and uses [`unsafe.Add`](https://pkg.go.dev/unsafe#Add) for offsets instead of storing pointers in integer variables across operations.
- HTTP and SOCKS share immutable credential records and synchronized exact-case lookup. Successful authentication carries the actual record's user pointer; removing the last governed account preserves mandatory authentication.
- Hysteria's UUID route-byte metadata clone retains its original authenticated user pointer. Routing metadata cannot select a replacement credential proof by name.
- Shadowsocks 2022 carries authenticated user pointers instead of mutable array indexes, checks user-publication errors, and keeps an explicitly empty client list in multi-user authentication mode. Its `sing-shadowsocks` dependency publishes identity, key and response-cipher maps as one immutable snapshot.

The runtime binds the actual authenticated object to a canonical client ID and immutable admission proof before starting listeners or publishing replacement users. It retains unchanged objects during client edits and retires changed objects and their governed links. Listener, transport and routing changes still use the structural restart path. See [headless hot authentication](../../docs/node-hot-authentication.md) for the adapter boundaries.

## Qualification checkpoint

The complete **marker-7 Linux race suites passed**: enforcement in **24.735 seconds** and the Xray runtime package in **3.232 seconds**. Coverage includes hot VLESS, VMess, Trojan, Shadowsocks AEAD/2022 and Hysteria TCP/UDP fixtures, HTTP/mixed authentication, paused SOCKS alias reuse and Hysteria route-byte identity.

SS2022 now has a regression that drives its actual inbound `Process` over `net.Pipe`, pauses the second read after authentication, replaces the user and checks the captured identity. Another regression checks atomicity when user publication fails. Each protocol in the concurrent fixture exercises **32 fresh TCP and 32 fresh UDP authentications during 32 hot updates**. Two deterministic stream-copy closure and half-close tests also passed.

These results supersede the earlier scoped marker-4 and local marker-5 checkpoints. The earlier full runs exposed a short-leading-buffer accounting overcount, a protocol connection close/read-response race and a bridge EOF-propagation hang. The accounting bound and marker-7 bridge lifecycle fixes are included in the passing suites; the failed runs remain diagnostic evidence, not passes.

All **15 hot-runtime negative controls reproduced their expected failures**, including paused SS2022 authentication, stream-buffer closure, close ordering and half-close. The deterministic AnyTLS terminal-error control passed with synchronization and reproduced the race when its read lock was removed. The full sing-box Linux race package also passed in **4.395 seconds**. Latest scoped lint for Xray enforcement/runtime and `tools/nodecore`, and nested sing-box lint, reported **zero issues**. The complete patched-wrapper `internal/arch` suite passed in **6.661 seconds** after the installer line-ending correction. Final artifact qualification remains pending.

Evidence is retained on the isolated verification host under `/tmp/penhoon-node-v2-20261008/scratchpad/node-v2/`: `hot-marker7-xray-linux.log`, `hot-singbox-full-linux.log`, `hot-marker7-negatives-linux.log`, and the per-control directory `hot-runtime-i7r1kt7v/`. See [headless hot authentication](../../docs/node-hot-authentication.md) for the remaining scope limits.

The regression fixtures cover shared accounting and quota exhaustion for VMess, VLESS, Trojan, Shadowsocks AEAD/2022, HTTP, mixed and Hysteria 2, with UDP where supported. The Vision fixture uses verified TLS 1.3 on both proxy transport and a local destination and compares governed usage with independently counted TLS record bytes. Linux race runs retain Go's pointer checks. `tools/nodekernel/xray-concurrency-negative.py` restores the original concurrency/pointer implementations in isolated overlays; `tools/nodekernel/hot-runtime-negative.py` exercises authentication safeguards. Fixture coverage is not a claim that the pending gates above, every transport, tunnel interface, internal bridge or optimized path are qualified.
