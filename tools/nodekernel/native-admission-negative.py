#!/usr/bin/env python3
import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path.cwd().resolve()
work = pathlib.Path(tempfile.mkdtemp(prefix="native-admission-negative-", dir=root / "scratchpad/node-v2"))
openvpn = "internal/cores/internal/openvpn/node_auth.go"
ocserv = "internal/cores/internal/ocserv/node.go"
cases = [
    ("openvpn-proof", openvpn,
     "return c != nil && (!c.scoped || c.proof.Current())", "return c != nil",
     "./internal/cores/internal/openvpn", "TestNodeManagementPausedCredentialCannotUseReassignedAlias", "paused credential acquired reused alias authority"),
    ("openvpn-receipt", openvpn,
     "\t\tif credential.scoped {\n\t\t\t_ = credential.proof.Request(ctx)\n\t\t}\n\t\treturn netip.Addr{}, errNodeAuthorization",
     "\t\tif credential.scoped {\n\t\t\t_ = credential.proof.Request(ctx)\n\t\t}\n\t\treturn a.authorize(ctx, e, now)",
     "./internal/cores/internal/openvpn", "TestNodeManagementAdmissionReceiptCannotAuthenticateOriginalAttempt", "management admission response="),
    ("ocserv-proof", ocserv,
     "return c != nil && (!c.scoped || c.proof.Current())", "return c != nil",
     "./internal/cores/internal/ocserv", "TestNodeRadiusPausedCredentialCannotUseReassignedAlias", "paused RADIUS credential acquired alias authority"),
    ("ocserv-receipt", ocserv,
     "\t\tif credential.scoped {\n\t\t\t_ = credential.proof.Request(ctx)\n\t\t}\n\t\treturn denied",
     "\t\tif credential.scoped {\n\t\t\t_ = credential.proof.Request(ctx)\n\t\t}\n\t\treturn a.Authenticate(ctx, username, password, radius.Peer{})",
     "./internal/cores/internal/ocserv", "TestNodeRadiusAdmissionReceiptCannotAcceptOriginalAttempt", "receipt accepted original RADIUS authentication="),
    ("radius-failure-classification", "internal/radius/server.go",
     "if authenticationFailure && s.Rejected != nil {", "if s.Rejected != nil {",
     "./internal/radius", "TestAuthenticatedAuthorizationDenialDoesNotCountAsFailedPassword", "authorization denial classification"),
    ("wireguard-unfunded", "internal/cores/noderuntime/native/authentication.go",
     'if grants[peer.ClientID].ID == "" {', 'if false && grants[peer.ClientID].ID == "" {',
     "./internal/cores/noderuntime/native", "TestNativeAuthenticationScopeRejectsMissingOrForeignSubjects", "unproven native authentication scope accepted="),
    ("native-descriptor-scope", "internal/cores/noderuntime/native/config.go",
     "if err := validateNativeAdmissions(out.config, runtime.Admissions, grants); err != nil {", "if err := error(nil); err != nil {",
     "./internal/cores/noderuntime/native", "TestNativeAuthenticationScopeRejectsMissingOrForeignSubjects", "unproven native authentication scope accepted="),
    ("native-proof-binding", "internal/cores/noderuntime/native/authentication.go",
     "if !found || proof.Descriptor() != descriptor {", "if false && (!found || proof.Descriptor() != descriptor) {",
     "./internal/cores/noderuntime/native", "TestNativeAuthenticationFailedRetryReplacesProofsAndRetainsUnchangedClient", "native candidate silently lost its proof binding="),
]
for name, filename, before, after, package, selection, evidence in cases:
    source = root / filename
    text = source.read_text()
    expected = 2 if name == "wireguard-unfunded" else 1
    if text.count(before) != expected:
        raise SystemExit("Mutation target changed: " + name)
    replacement = work / (name + ".go.txt")
    replacement.write_text(text.replace(before, after, 1))
    overlay = work / (name + ".json")
    overlay.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    result = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p", "1", "-count=1", "-timeout=90s", "-overlay", str(overlay), "-run", "^" + selection + "$", package],
                            cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (work / (name + ".log")).write_bytes(result.stdout)
    if result.returncode == 0 or evidence.encode() not in result.stdout or b"[build failed]" in result.stdout:
        raise SystemExit("Native admission negative did not detect " + name + "; inspect " + str(work / (name + ".log")))
    print("Detected native admission safeguard: " + name, flush=True)
print("Native admission negative evidence: " + str(work), flush=True)
