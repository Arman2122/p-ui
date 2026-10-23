#!/usr/bin/env python3
import json
import os
import pathlib
import subprocess
import tempfile

if not os.environ.get('PUI_DB_DSN'):
    raise SystemExit('PUI_DB_DSN is required; skipped tests cannot qualify address allocation.')
root = pathlib.Path.cwd().resolve()
work = root / 'scratchpad/node-v2'
work.mkdir(parents=True, exist_ok=True)
cases = [
    ('shaping-hop', 'internal/web/service/fleet_native_binding.go',
     'occupied[network.PeerIPv4], occupied[network.GatewayIPv4] = "shaping-peer", "shaping-gateway"',
     '_ = network', 'TestFleetNativeAddressAllocationReservesConfiguredShapingHop', 'shaping hop'),
    ('atomic-publication', 'internal/web/service/fleet_network_publish.go',
     'if err := persistFleetNativeBindings(tx, state.Node, revision, current.NativeBindings); err != nil {',
     'if err := error(nil); err != nil {', 'TestFleetNativePublicationCommitsAddressesWithDeployment', 'published address='),
    ('applied-removal', 'internal/web/service/fleet_native_retirement.go',
     ' || node.AppliedRevision != node.DesiredRevision', '',
     'TestFleetNativeAddressReuseRequiresAppliedRemovalAndEveryStoppedGrant', 'unsafe address release='),
    ('every-stopped-grant', 'internal/web/service/fleet_native_retirement.go',
     ' && stopped[row.PlacementID]', ' && len(stopped) >= 0',
     'TestFleetNativeAddressReuseRequiresAppliedRemovalAndEveryStoppedGrant', 'unsafe address release='),
    ('inactive-removal', 'internal/web/service/fleet_network_sources.go',
     'if stopped[placement.ID] {', 'if len(stopped) >= 0 {',
     'TestFleetNativeAddressReuseRequiresAppliedRemovalAndEveryStoppedGrant', 'unstopped native removal ready='),
    ('immutable-target', 'internal/web/service/fleet_native_binding.go',
     ' || existing.TargetID != row.TargetID', '',
     'TestFleetNativePublicationCommitsAddressesWithDeployment', 'published address relabeled='),
]
for name, filename, before, after, test, evidence in cases:
    source = (root / filename).read_text()
    if source.count(before) != 1:
        raise SystemExit('Mutation target changed: ' + name)
    destination = pathlib.Path(tempfile.mkdtemp(prefix='native-binding-negative-' + name + '-', dir=work))
    replacement = destination / pathlib.Path(filename).name
    replacement.write_text(source.replace(before, after, 1))
    overlay = destination / 'overlay.json'
    overlay.write_text(json.dumps({'Replace': {str(root / filename): str(replacement)}}))
    result = subprocess.run(['go', 'run', './tools/nodecore', '-core', 'xray', '-go', 'test', '--',
        '-overlay', str(overlay), './internal/web/service', '-run', '^' + test + '$', '-count=1'],
        cwd=root, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (destination / 'result.log').write_bytes(result.stdout)
    if result.returncode == 0 or evidence.encode() not in result.stdout or b'[build failed]' in result.stdout:
        raise SystemExit('Negative control did not detect ' + name + '; inspect ' + str(destination / 'result.log'))
    print('Detected removed safeguard: ' + name)
