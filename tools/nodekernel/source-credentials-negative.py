import json
import os
import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "scratchpad" / "node-v2" / "source-credentials-negative"
OUTPUT.mkdir(parents=True, exist_ok=True)
ADAPTERS = "internal/cores/internal/"
CASES = [
    ("no-replacement", "internal/core/source_credentials.go",
     'if supplied != value {', 'if false && supplied != value {',
     "./internal/cores", "^TestSourceCredentialsRejectSuppliedValuesThatLegacyWouldRepair$"),
    ("openvpn-host-mint", ADAPTERS + "openvpn/source_credentials.go",
     'result, err := core.SourceCredentialAdditions(have, mintLoginCredentials(have))',
     'minted, mintErr := c.MintClientCredentials(kind, `{"tlsCryptV2ServerKey":"present"}`, have)\n\tif mintErr != nil { return core.SourceCredentialResult{}, mintErr }\n\tresult, err := core.SourceCredentialAdditions(have, minted)',
     "./internal/cores/internal/openvpn", "^TestSourceCredentialsOpenVPNNeverInvokesHostKeyGeneration$"),
    ("openvpn-node-dependency", ADAPTERS + "openvpn/source_credentials.go",
     'if have[core.CredTLSCryptV2] == "" {', 'if false {',
     "./internal/cores", "^TestSourceCredentialsMintValidateAndReplayWithoutReplacing$/^openvpn$"),
    ("xray-account", ADAPTERS + "xray/source_credentials.go",
     'return []string{core.CredUsername, core.CredPassword}', 'return c.ClientCredentials(kind)',
     "./internal/cores/nodeconfig", "^TestSourceCredentialsXrayAccountMatchesNodeCompiler$"),
    ("uuid-shape", ADAPTERS + "xray/source_credentials.go",
     'if err != nil || parsed == uuid.Nil || parsed.String() != have[core.CredUUID] {',
     'if false && (err != nil || parsed == uuid.Nil || parsed.String() != have[core.CredUUID]) {',
     "./internal/cores", "^TestSourceCredentialsValidateActualNodeFormats$/^(uuid|nil_UUID)$"),
    ("ss2022-size", ADAPTERS + "xray/source_credentials.go",
     'if !engine.ValidShadowsocksClientKey(engine.ShadowsocksMethodFromSettings(settings), have[core.CredPassword]) {',
     'if false && !engine.ValidShadowsocksClientKey(engine.ShadowsocksMethodFromSettings(settings), have[core.CredPassword]) {',
     "./internal/cores", "^TestSourceCredentialsValidateActualNodeFormats$/^ss2022_size$"),
    ("faketls-zero", ADAPTERS + "mtproto/source_credentials.go",
     ' || bytes.Equal(decoded[1:17], make([]byte, 16))', ' || false && bytes.Equal(decoded[1:17], make([]byte, 16))',
     "./internal/cores", "^TestSourceCredentialsValidateActualNodeFormats$/^FakeTLS_zero_key$"),
    ("wireguard-key", ADAPTERS + "wireguard/source_credentials.go",
     'if err != nil || key == (wgtypes.Key{}) || key.String() != value {',
     'if false && (err != nil || key == (wgtypes.Key{}) || key.String() != value) {',
     "./internal/cores", "^TestSourceCredentialsValidateActualNodeFormats$/^(WG_public_key|AWG_zero_key|WG_shared_key)$"),
    ("wireguard-prefix", ADAPTERS + "wireguard/source_credentials.go",
     'if _, err := netip.ParsePrefix(strings.TrimSpace(value)); err != nil {',
     'if _, err := netip.ParsePrefix(strings.TrimSpace(value)); false && err != nil {',
     "./internal/cores/nodeconfig", "^TestSourceCredentialsWireGuardAllowedIPsMatchNodeCompiler$"),
    ("wireguard-prefix-bound", ADAPTERS + "wireguard/source_credentials.go",
     'if len(raw) > 4096 || strings.Count(raw, ",") >= 128 {',
     'if false && (len(raw) > 4096 || strings.Count(raw, ",") >= 128) {',
     "./internal/cores/nodeconfig", "^TestSourceCredentialsWireGuardAllowedIPsMatchNodeCompiler$"),
    ("openvpn-password", ADAPTERS + "openvpn/source_credentials.go",
     'if len(have[core.CredPassword]) > 512 || strings.ContainsAny(have[core.CredPassword], "\\r\\n\\x00") {',
     'if false && (len(have[core.CredPassword]) > 512 || strings.ContainsAny(have[core.CredPassword], "\\r\\n\\x00")) {',
     "./internal/cores", "^TestSourceCredentialsValidateActualNodeFormats$/^OpenVPN_newline$"),
    ("ocserv-password", ADAPTERS + "ocserv/source_credentials.go",
     'if len(have[core.CredPassword]) > 512 || strings.ContainsAny(have[core.CredPassword], "\\r\\n\\x00") {',
     'if false && (len(have[core.CredPassword]) > 512 || strings.ContainsAny(have[core.CredPassword], "\\r\\n\\x00")) {',
     "./internal/cores", "^TestSourceCredentialsValidateActualNodeFormats$/^(ocserv_long_password|ocserv_NUL)$"),
]


def main():
    environment = {**os.environ, "GOMAXPROCS": "2"}
    prepared = subprocess.run(["go", "run", "./tools/nodecore", "-core", "xray", "-prepare-only"],
                              cwd=ROOT, env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    module = pathlib.Path(prepared.stdout.strip()).resolve()
    module.relative_to((ROOT / ".cache" / "node-core").resolve())
    if module.name != "runtime.mod" or not module.is_file():
        raise RuntimeError("unexpected patched module")
    for name, relative, before, after, package, selector in CASES:
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
            if result.returncode == 0 or "--- FAIL:" not in result.stdout or "[build failed]" in result.stdout:
                raise RuntimeError(f"{name}: removed safeguard did not fail the intended test; see its log")
            if source.read_text(encoding="utf-8") != original:
                raise RuntimeError(f"{name}: production source changed during qualification")
            print(f"PASS {name}: removed safeguard rejected", flush=True)


if __name__ == "__main__":
    main()

