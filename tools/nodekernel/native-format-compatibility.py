import json
import os
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
output = pathlib.Path(tempfile.mkdtemp(prefix="native-format3-", dir=root / "scratchpad/node-v2"))
startup = "TestManagedWorkerRefusesIncompatibleStateBeforeBackend"
native = "TestNativeJournalRefusesIncompatibleFormatWithoutChangingState"
guard = 'len(version) != 8 || binary.BigEndian.Uint64(version) != uint64(compatibility.StateFormat)'
platform = 'if runtime.GOOS != "linux" {'
cases = [
    ("startup-current", "./internal/node/command", startup, [], True, "--- PASS: " + startup),
    ("startup-format2-reader", "./internal/node/command", startup, [("internal/node/compatibility/version.go", "StateFormat uint32 = 3", "StateFormat uint32 = 2")], True, "--- PASS: " + startup + "/3"),
    ("startup-removed-floor", "./internal/node/command", startup, [("internal/node/state/store.go", guard, "false && (" + guard + ")")], False, "incompatible state reached backend: called=true"),
    ("native-removed-floor", "./internal/node/native", native, [("internal/node/native/journal.go", guard, "false && (" + guard + ")")], False, "incompatible native journal reopened"),
    ("release-removed-floor", "./internal/node/release", "TestReleaseRejectsTamperingPlatformAndChangedAssets", [("internal/node/release/manifest.go", " || m.StateFormat != compatibility.StateFormat", "")], False, "unsupported format 1 verified"),
]
environment = dict(os.environ, GOMAXPROCS="2")
for name, package, test, replacements, positive, expected in cases:
    replacements = list(replacements)
    if os.name == "nt" and package == "./internal/node/command":
        check = platform + '\n\t\treturn errors.New("headless runtimes require Linux")\n\t}'
        replacements += [("internal/node/command/command.go", check, check.replace("if runtime", "if false && runtime")), ("internal/node/command/compatibility_test.go", platform, 'if false && runtime.GOOS != "linux" {')]
    mapping = {}
    for index, (relative, before, after) in enumerate(replacements):
        path = root / relative
        content = path.read_text()
        if content.count(before) != 1:
            raise SystemExit("compatibility overlay anchor changed: " + name + ": " + relative)
        changed = output / f"{name}-{index}.go.txt"
        changed.write_text(content.replace(before, after))
        mapping[str(path)] = str(changed)
    overlay = output / (name + ".json")
    overlay.write_text(json.dumps({"Replace": mapping}))
    command = ["go", "run", "./tools/nodecore", "-core", "xray", "-go", "test", "--", "-p=1", "-count=1", "-v", "-timeout=90s", "-overlay", str(overlay), "-run", "^" + test + "$", package]
    result = subprocess.run(command, cwd=root, env=environment, capture_output=True, text=True, timeout=300)
    log = result.stdout + result.stderr
    (output / (name + ".log")).write_text(log)
    if (result.returncode == 0) != positive or expected not in log or "--- SKIP:" in log:
        raise SystemExit("compatibility case did not reproduce: " + name + "\n" + log)
    print("compatibility case verified: " + name, flush=True)
print(output)
