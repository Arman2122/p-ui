import json
import os
import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "scratchpad" / "node-v2" / "source-claims-negative"
OUTPUT.mkdir(parents=True, exist_ok=True)
XRAY = "internal/cores/internal/xray/source_claims.go"
CASES = [
    ("vless-mask", XRAY, "key := vless.ProcessUUID(id)", "key := vless.ProcessUUID(id); key = [16]byte(id)",
     "./internal/cores/internal/xray", "^TestSourceClaimsXrayMaskedUUIDsMatchActualValidators$/^vless$", "actual authentication alias received a distinct claim"),
    ("hysteria-mask", XRAY, "\t\t\tid[6], id[7] = 0, 0\n", "",
     "./internal/cores/internal/xray", "^TestSourceClaimsXrayMaskedUUIDsMatchActualValidators$/^hysteria$", "actual authentication alias received a distinct claim"),
    ("vmess-key", XRAY, "protocol.NewID(xuuid.UUID(id)).CmdKey()", "protocol.NewID(xuuid.UUID(id)).Bytes()",
     "./internal/cores/internal/xray", "^TestSourceClaimsVMessAndTrojanMatchAuthenticatedLookupKeys$", "VMess claim does not use the actual command key"),
    ("trojan-wire-key", XRAY, "[]byte(hex.EncodeToString(key[:]))", "[]byte(hex.EncodeToString(key[:]))[:28]",
     "./internal/cores/internal/xray", "^TestSourceClaimsVMessAndTrojanMatchAuthenticatedLookupKeys$", "Trojan claim does not use its actual wire lookup key"),
    ("ss2022-key", XRAY, "lookup := blake3.Sum512(key)", "lookup := blake3.Sum512(append([]byte(have[core.CredPassword]), key...))",
     "./internal/cores/internal/xray", "^TestSourceClaimsSS2022AliasesMatchActualAuthentication$", "wire-equivalent SS2022 keys reserved different identities"),
    ("ss-cipher-alias", "internal/xray/shadowsocks.go", "return append(identity, memory.Key...), nil",
     "return append(append(identity, []byte(method)...), memory.Key...), nil",
     "./internal/cores/internal/xray", "^TestSourceClaimsLegacyShadowsocksUsesEffectiveCipherAndKey$", "changes its actual authentication claim"),
    ("mtproto-domain", "internal/cores/internal/mtproto/source_claims.go", "key[1:17]", "key[1:]",
     "./internal/cores/nodeconfig", "^TestSourceClaimsMatchNodeAuthenticationCollisions$/^mtproto$", "same node login identity acquired different source claims"),
    ("tuic-spelling", "internal/cores/internal/singbox/source_claims.go", '"singbox-tuic-uuid-v1", id[:]',
     '"singbox-tuic-uuid-v1", append(id[:], []byte(have[core.CredUUID])...)',
     "./internal/cores", "^TestSourceCredentialClaimsSharedConformanceFixtures$/^tuic-", "authentication claim differs from pinned conformance fixture"),
    ("username", "internal/cores/internal/ssh/source_claims.go", "[]byte(have[core.CredUsername])", "[]byte(email)",
     "./internal/cores", "^TestSourceCredentialClaimsUseLoginNamesWithoutPasswordOrConfigEmail$/^ssh$", "same login acquired a second identity"),
    ("validation-first", "internal/cores/internal/openvpn/source_claims.go",
     "\tif err := c.ValidateSourceClientCredentials(kind, settings, email, have); err != nil {\n\t\treturn nil, err\n\t}\n", "",
     "./internal/cores", "^TestSourceCredentialClaimsValidateBeforeReservingIdentity$/^openvpn$", "invalid credential reserved an identity"),
    ("public-key", "internal/cores/internal/wireguard/source_claims.go", "wgtypes.ParseKey(have[core.CredPublicKey])", "wgtypes.ParseKey(have[core.CredPreSharedKey])",
     "./internal/cores/nodeconfig", "^TestSourceClaimsMatchNodeAuthenticationCollisions$/^(wgkernel|awgkernel)$", "same node login identity acquired different source claims"),
    ("http-fallback", XRAY, "username = email", 'username = "missing-user"',
     "./internal/cores/nodeconfig", "^TestSourceClaimsMatchNodeAuthenticationCollisions$/^(http|mixed)$", "same node login identity acquired different source claims"),
]


def main():
    environment = {**os.environ, "GOMAXPROCS": "2"}
    prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"],
                              cwd=ROOT, env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    module = pathlib.Path(prepared.stdout.strip()).resolve()
    module.relative_to((ROOT / ".cache" / "node-core").resolve())
    if module.name != "runtime.mod" or not module.is_file():
        raise RuntimeError("unexpected patched module")
    for name, relative, before, after, package, selector, failure in CASES:
        if len(sys.argv) > 1 and name not in sys.argv[1:]:
            continue
        source = ROOT / relative
        original = source.read_text(encoding="utf-8")
        if original.count(before) != 1:
            raise RuntimeError(f"{name}: expected exactly one guard")
        with tempfile.TemporaryDirectory(prefix=f"{name}-", dir=OUTPUT) as temporary:
            directory = pathlib.Path(temporary).resolve()
            directory.relative_to(OUTPUT.resolve())
            mutated = directory / source.name
            mutated.write_text(original.replace(before, after, 1), encoding="utf-8")
            overlay = directory / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(source): str(mutated)}}), encoding="utf-8")
            command = ["go", "test", "-p=1", "-modfile", str(module), "-overlay", str(overlay),
                       "-count=1", "-timeout=60s", "-run", selector, package]
            result = subprocess.run(command, cwd=ROOT, env=environment, text=True,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
            (OUTPUT / f"{name}.log").write_text(result.stdout, encoding="utf-8")
            if result.returncode == 0 or "--- FAIL:" not in result.stdout or failure not in result.stdout or "[build failed]" in result.stdout:
                raise RuntimeError(f"{name}: removed safeguard did not fail the intended assertion; see its log")
            if source.read_text(encoding="utf-8") != original:
                raise RuntimeError(f"{name}: production source changed during qualification")
            print(f"PASS {name}: removed safeguard rejected", flush=True)


if __name__ == "__main__":
    main()

