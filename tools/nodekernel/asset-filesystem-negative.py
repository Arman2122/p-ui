import json
import os
import pathlib
import re
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[2]
if os.geteuid() != 0:
    raise SystemExit("asset filesystem qualification requires root for the temp-file foreign-owner fixture")
output = pathlib.Path(tempfile.mkdtemp(prefix="asset-filesystem-", dir=root / "scratchpad/node-v2"))
source = root / "internal/node/assets/store_linux.go"
original = source.read_text()
cases = [
    ("ancestor-symlink", "resolve := uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS)", "resolve := uint64(0)", "TestStoreRefusesSymlinkedOrReplacedDirectories/parent-symlink", "ancestor symlink was accepted"),
    ("anchor", "if !sameDirectory(root, s.root) || !sameDirectory(dir, s.dir) {", "if false && (!sameDirectory(root, s.root) || !sameDirectory(dir, s.dir)) {", "TestStoreRefusesSymlinkedOrReplacedDirectories/assets-replaced", "replaced or insecure directory accepted"),
    ("hardlink", " || stat.Nlink != 1", "", "TestStageRefusesUnsafeExistingFilesWithoutReplacingThem/hardlink", "unsafe hardlink asset was admitted"),
    ("mode", " || stat.Mode&0o7777 != 0o600", "", "TestStageRefusesUnsafeExistingFilesWithoutReplacingThem/mode", "unsafe mode asset was admitted"),
    ("owner", " || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1", " || stat.Nlink != 1", "TestStageRefusesUnsafeExistingFilesWithoutReplacingThem/owner", "unsafe owner asset was admitted"),
    ("content", " || hex.EncodeToString(hash.Sum(nil)) != name", "", "TestStageRefusesUnsafeExistingFilesWithoutReplacingThem/content", "unsafe content asset was admitted"),
    ("retained", "if retained[name] {", "if false && retained[name] {", "TestPrunePreservesAppliedAndDesiredAndRemovesBoundedOrphans", "cleanup removed applied or desired authority"),
    ("temp-cleanup", "unix.Unlinkat(int(s.dir.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT)", "error(nil); err != nil && !errors.Is(err, unix.ENOENT)", "TestStageCleansFailedTemporaryCreation", "failed staged creation leaked temporary files"),
]

def overlay(name, before, after):
    if original.count(before) != 1:
        raise RuntimeError(f"asset overlay context changed: {name}")
    replacement, mapping = output / f"{name}.go", output / f"{name}.json"
    replacement.write_text(original.replace(before, after))
    mapping.write_text(json.dumps({"Replace": {str(source): str(replacement)}}))
    return mapping

for name, before, after, test, failure in cases:
    mapping = overlay(name, before, after)
    result = subprocess.run(["go", "test", "-race", "-count=1", "-timeout=30s", "-overlay", str(mapping), "-run", f"^{test}$", "./internal/node/assets"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (output / f"{name}.log").write_text(result.stdout)
    if result.returncode == 0 or failure not in result.stdout:
        raise RuntimeError(f"asset removed-guard test did not fail correctly: {name}\n{result.stdout}")
    print(f"asset negative control reproduced: {name}", flush=True)

def durable_trace(name, mapping=None):
    binary, trace = output / f"{name}.test", output / f"{name}.strace"
    command = ["go", "test", "-c", "-o", str(binary)]
    if mapping:
        command += ["-overlay", str(mapping)]
    subprocess.run(command + ["./internal/node/assets"], cwd=root, check=True, capture_output=True)
    result = subprocess.run(["strace", "-f", "-yy", "-e", "trace=openat2,fsync,renameat2,unlinkat", "-o", str(trace), str(binary), "-test.run=^TestStageKeepsImmutableAppliedAndCandidateFiles$"], cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    (output / f"{name}.log").write_text(result.stdout)
    if result.returncode != 0:
        raise RuntimeError(f"asset syscall fixture failed: {name}\n{result.stdout}")
    lines = trace.read_text().splitlines()
    publications = []
    for index, line in enumerate(lines):
        found = re.search(r'renameat2\(\d+<([^>]+)>, "(\.stage-[0-9a-f]{32})", \d+<([^>]+)>, "([0-9a-f]{64})", RENAME_NOREPLACE\) = 0', line)
        if found:
            publications.append((index, found.group(1), found.group(2), found.group(3)))
    if len(publications) != 2:
        return False
    for item, (index, source_dir, temporary, target_dir) in enumerate(publications):
        end = publications[item + 1][0] if item + 1 < len(publications) else len(lines)
        file_sync = re.compile(r'fsync\(\d+<' + re.escape(source_dir + "/" + temporary) + r'>\) = 0')
        dir_sync = re.compile(r'fsync\(\d+<' + re.escape(target_dir) + r'>\) = 0')
        if not any(file_sync.search(line) for line in lines[:index]) or not any(dir_sync.search(line) for line in lines[index + 1:end]):
            return False
    return True

if not durable_trace("durable-positive"):
    raise RuntimeError("asset publication lacked file fsync -> noreplace rename -> directory fsync")
print("asset publication syscall order qualified", flush=True)
for name, before, after in [
    ("file-sync", "errors.Join(writeErr, file.Sync(), file.Close())", "errors.Join(writeErr, file.Close())"),
    ("no-replace", "unix.RENAME_NOREPLACE", "0"),
    ("directory-sync", "errors.Join(s.dir.Sync(), s.checkAnchor()); err != nil", "s.checkAnchor(); err != nil"),
]:
    if durable_trace(name, overlay(name, before, after)):
        raise RuntimeError(f"asset durability trace did not detect missing {name}")
    print(f"asset durability negative control reproduced: {name}", flush=True)
print(f"Asset filesystem qualified with eight semantic and three syscall-order negative controls: {output}")
