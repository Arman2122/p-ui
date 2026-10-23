import argparse
import json
import os
import pathlib
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--local", action="store_true", help="run only socket-free immutable-handle regressions")
parser.add_argument("--no-race", action="store_true")
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="hot-runtime-", dir=root / "scratchpad/node-v2"))
environment = dict(os.environ, GOFLAGS="-trimpath")
modules = {}
cases = [
    ("dependency-source-drift", "nodecore", "tools/nodecore/dependencies.go", [("hex.EncodeToString(digest[:]) != file.SHA256", "hex.EncodeToString(digest[:]) != file.SHA256 && false")], "TestAuthenticationDependencyPreparationRejectsSourceDrift", "authentication dependency accepted checksum drift", True),
    ("xray-normalized-proof", "xray", "internal/cores/internal/xray/enforcement/dispatcher.go", [("if !authenticated.current() {", "if authenticated == nil {")], "TestXrayFundedAuthenticationCannotRebindRetiredPointer", "normalized old identity bypassed its retired proof", True),
    ("xray-funded-flow", "xray", "internal/cores/internal/xray/enforcement/buffers.go", [("authenticated != nil && !authenticated.current()", "authenticated != nil && !authenticated.current() && false")], "TestXrayFundedAuthenticationCannotRebindRetiredPointer", "retired authentication forwarded with an existing funded account", True),
    ("xray-pointer-relabel", "xray", "internal/cores/internal/xray/enforcement/authentication.go", [("record := r.users[user]\n\tif record == nil", "record := r.users[user]\n if record == nil || !record.current() { for candidate, replacement := range r.users { if candidate.Email == user.Email && replacement.current() { record = replacement; break } } }\n\tif record == nil")], "TestXrayFundedAuthenticationCannotRebindRetiredPointer", "retired funded authentication was rebound by login", True),
    ("ss2022-paused-relookup", "xray", "@upstream/proxy/shadowsocks_2022/inbound_multi.go", [("func (i *MultiUserInbound) NewConnection(ctx context.Context, conn net.Conn, metadata M.Metadata) error {", "func (i *MultiUserInbound) NewConnection(ctx context.Context, conn net.Conn, metadata M.Metadata) error {\n captured, _ := A.UserFromContext[*protocol.MemoryUser](ctx)\n ctx = A.ContextWithUser(ctx, i.GetUser(ctx, captured.Email))")], "TestXrayShadowsocks2022PausedAuthenticationKeepsOriginalUser", "paused SS2022 authentication changed identity after user replacement", True),
    ("stream-buffer-close-order", "xray", "@upstream/common/singbridge/pipe.go", [("serverConn = &copyTransportConn{Conn: serverConn, raw: raw}", "_ = raw")], "TestXrayStreamCopyJoinsReadsBeforeReleasingBuffers", "stream buffers released before copy tasks joined", True),
    ("stream-half-close", "xray", "@upstream/common/singbridge/pipe.go", [("func (w *PipeConnWrapper) CloseWrite()", "func (w *PipeConnWrapper) CloseWrites()")], "TestXrayStreamCopyPreservesResponseAfterClientHalfClose", "client half-close discarded pending response", True),
    ("singbox-carrier-relabel", "singbox", "core-runtimes/singbox/authentication.go", [("record := r.users[handle]\n\tif record == nil", "record := r.users[handle]\n if record == nil || !record.current() { for candidate, replacement := range r.users { if authenticationName(candidate) == authenticationName(handle) && replacement.current() { record = replacement; break } } }\n\tif record == nil")], "TestSingboxFundedAuthenticationRetainsExactHandle", "old funded carrier rebound by mutable user name", True),
    ("xray-forced-restart", "xray", "internal/cores/noderuntime/xray/runtime.go", [("if x.instance != nil && x.structure != nil", "if false && x.instance != nil && x.structure != nil")], "TestXrayHotMixedPreservesUnchangedTrafficAndRetiresPausedAuthentication", "unchanged Xray listener restarted during hot authentication", False),
    ("singbox-forced-restart", "singbox", "core-runtimes/singbox/managed.go", [("if m.instance != nil && bytes.Equal", "if false && m.instance != nil && bytes.Equal")], "TestSingboxHotAuthenticationPreservesTCPAndUDP", "hot sing-box authentication restarted unchanged runtime", False),
    ("socks-paused-relookup", "xray", "@upstream/proxy/socks/protocol.go", [("request.User = s.authenticated", "request.User = s.credentials.GetUser(context.Background(), username)")], "TestXrayHotMixedPreservesUnchangedTrafficAndRetiresPausedAuthentication", "paused old authentication used reused funded alias", False),
    ("http-empty-anonymous", "xray", "@upstream/proxy/http/config.go", [("return s.required", "return false")], "TestXrayHotHTTPEmptyUsersStillRequireAuthentication", "empty governed HTTP credential store allowed anonymous authentication", False),
    ("hysteria-route-proof", "xray", "@upstream/proxy/hysteria/account/config.go", [("return a.authenticated", "return nil")], "TestXrayHotProtocolsPreserveUnchangedTCPAndUDP/hysteria", "unchanged protocol connection interrupted", False),
    ("singbox-cache-retirement-race", "singbox", "core-runtimes/singbox/tracker.go", [("c = ownAuthenticationCache(c)", "_ = c")], "TestSingboxHotAuthenticationPreservesTCPAndUDP/tuic", "WARNING: DATA RACE", False),
    ("anytls-terminal-error-race", "singbox", "@anytls/session/stream.go", [("s.dieErrMu.RLock()", ""), ("defer s.dieErrMu.RUnlock()", "")], "TestPenhoonStreamCloseErrorPublication", "WARNING: DATA RACE", False),
]
try:
    for name, family, relative, edits, selection, expected, local in cases:
        if args.local and not local:
            continue
        prepared_family = "xray" if family == "nodecore" else family
        if prepared_family not in modules:
            prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", prepared_family, "-prepare-only"], cwd=root, env=environment, check=True, capture_output=True, text=True)
            module = pathlib.Path(prepared.stdout.strip()).resolve()
            if module.parent.parent != (root / ".cache/node-core").resolve() or not module.parent.name.startswith(prepared_family + "-"):
                raise RuntimeError("unexpected prepared authentication module")
            modules[prepared_family] = module
        module = modules[prepared_family]
        source = module.parent / "source" / relative.removeprefix("@upstream/") if relative.startswith("@upstream/") else root / relative
        if relative.startswith("@anytls/"):
            source = module.parent / "dependency-1" / relative.removeprefix("@anytls/")
        data = source.read_text()
        for before, after in edits:
            if data.count(before) != 1:
                raise RuntimeError("authentication overlay context changed: " + name)
            data = data.replace(before, after)
        replacement, overlay = output / (name + ".go"), output / (name + ".json")
        replacement.write_text(data)
        overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
        count = "3" if expected == "WARNING: DATA RACE" else "1"
        command = ["go", "test", "-count=" + count, "-timeout=60s", "-modfile", str(module), "-overlay", str(overlay), "-run", "^" + selection + "$"]
        if not args.no_race:
            command += ["-race"]
        if family == "singbox":
            package = "."
            if name == "anytls-terminal-error-race":
                package = "github.com/anytls/sing-anytls/session"
                regression = module.parent / "dependency-1/session/penhoon_hot_error_test.go"
                regression.write_text('''package session
import("errors";"net";"runtime";"testing")
func TestPenhoonStreamCloseErrorPublication(t *testing.T) {
 for range 64 {
  stream := newStream(1, nil)
  started, result := make(chan struct{}), make(chan error, 1)
  go func(){ close(started); for { if err := stream.closedError(); err != nil { result <- err; return }; runtime.Gosched() } }()
  <-started
  stream.closeLocally()
  if err := <-result; !errors.Is(err, net.ErrClosed) { t.Fatalf("stream terminal state=%v", err) }
 }
}
''')
                baseline = ["go", "test", "-race", "-count=1", "-timeout=30s", "-modfile", str(module), "-run", "^" + selection + "$", package]
                positive = subprocess.run(baseline, cwd=root / "core-runtimes/singbox", env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
                (output / (name + "-positive.log")).write_text(positive.stdout)
                if positive.returncode != 0:
                    raise RuntimeError("stream terminal-state baseline failed: " + positive.stdout)
            command += ["-tags", "with_quic", package]
            cwd = root / "core-runtimes/singbox"
        elif family == "nodecore":
            command += ["./tools/nodecore"]
            cwd = root
        else:
            command += ["./internal/cores/internal/xray/enforcement", "./internal/cores/noderuntime/xray"]
            cwd = root
        result = subprocess.run(command, cwd=cwd, env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        (output / (name + ".log")).write_text(result.stdout)
        if result.returncode == 0 or expected not in result.stdout:
            raise RuntimeError("authentication regression missed removed guard: " + name + "\n" + result.stdout[-6000:])
        print("hot-runtime negative control reproduced: " + name, flush=True)
finally:
    for module in modules.values():
        shutil.rmtree(module.parent)
print("Hot runtime immutable authentication negative controls: " + str(output), flush=True)
