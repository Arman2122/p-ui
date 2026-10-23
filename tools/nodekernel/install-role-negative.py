#!/usr/bin/env python3
import pathlib
import shutil
import subprocess
import tempfile

root = pathlib.Path.cwd().resolve()
work = root / 'scratchpad/node-v2'
work.mkdir(parents=True, exist_ok=True)
source = (root / 'deploy/install.py').read_text()
mutations = {
    'role-conversion': ('if record.get("role") != role:', 'if False:'),
    'request-remint': ('if path.exists() or path.is_symlink():', 'if False:'),
    'private-bootstrap': ('info.st_mode & (0o077 if private else 0o022)', 'False'),
    'loopback-tls-name': ('config["serverName"] = name', 'config["serverName"] = ""'),
}
for name, (before, after) in mutations.items():
    if source.count(before) != 1:
        raise SystemExit('Mutation target changed: ' + name)
    destination = pathlib.Path(tempfile.mkdtemp(prefix='role-negative-' + name + '-', dir=work))
    shutil.copytree(root / 'deploy', destination / 'deploy', ignore=shutil.ignore_patterns('__pycache__'))
    tests = destination / 'tools/nodekernel'
    tests.mkdir(parents=True)
    for filename in ('install-role-test.py', 'install-role-test.sh'):
        shutil.copyfile(root / 'tools/nodekernel' / filename, tests / filename)
    (destination / 'deploy/install.py').write_text(source.replace(before, after, 1))
    result = subprocess.run(['unshare', '--mount', '--propagation', 'private', 'bash', 'tools/nodekernel/install-role-test.sh'], cwd=destination, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (destination / 'result.log').write_bytes(result.stdout)
    if result.returncode == 0 or b'FAILED (' not in result.stdout:
        raise SystemExit('Negative control did not detect ' + name + '; inspect ' + str(destination / 'result.log'))
    print('Detected removed safeguard: ' + name)
