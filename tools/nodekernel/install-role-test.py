#!/usr/bin/env python3
import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import shutil
import stat
import subprocess
import tempfile
import types
import unittest
from unittest.mock import patch

if os.geteuid() != 0 or os.readlink('/proc/self/ns/mnt') == os.readlink('/proc/1/ns/mnt'):
    raise SystemExit('Role fixture requires a private root mount namespace.')
for target in ('/etc', '/var/lib', '/var/log', '/usr/local', '/run'):
    if not any(line.split()[1] == target and line.split()[0] == 'penhoon-role-fixture' for line in pathlib.Path('/proc/mounts').read_text().splitlines()):
        raise SystemExit('Role fixture system directories must be masked.')
spec = importlib.util.spec_from_file_location('role_installer', pathlib.Path('deploy/install.py').resolve())
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class RoleInstallation(unittest.TestCase):
    def setUp(self):
        for parent in ('/var/lib', '/var/log', '/usr/local'):
            for path in pathlib.Path(parent).iterdir():
                if path.is_dir() and not path.is_symlink():
                    shutil.rmtree(path)
                else:
                    path.unlink()
        for path in (pathlib.Path('/etc/penhoon'), pathlib.Path('/etc/p-ui'), pathlib.Path('/etc/systemd')):
            if path.exists():
                shutil.rmtree(path)
        self.work = pathlib.Path(tempfile.mkdtemp(dir='/run', prefix='role-inputs-'))
        binary = self.work / 'p-ui'
        binary.write_bytes(b'authenticated test panel')
        dsn = self.work / 'dsn'
        dsn.write_text('postgres://role-fixture:private@localhost/test')
        dsn.chmod(0o600)
        self.options = argparse.Namespace(role='master', panel_binary=str(binary), panel_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
            database_dsn_file=str(dsn), control_listen=':7443', enrollment_listen=':7444', control_address='control.example:7443',
            enrollment_url='https://control.example:7444/enroll', server_name='control.example', panel_command=None, panel_arguments=[],
            release_dir='/signed/release', trusted_key='/trusted/publisher', tuf_root='/trusted/root', metadata_url='https://updates.example/metadata',
            targets_url='https://updates.example/targets', enrollment=None, native=False, dry_run=False)
        self.commands = []
        self.enroll_requests = []
        self.node_installs = []
        self.fail_response = False
        self.fail_node = False
        self.enrollments = 0
        self.patches = [patch.object(installer, 'run', self.run_command), patch.object(installer.node, 'account', self.account),
                        patch.object(installer.node.shutil, 'which', lambda *args, **kwargs: '/fixture/command'),
                        patch.object(installer, 'verify_node_inputs', lambda options: None),
                        patch.object(installer.node, 'install', self.install_node), patch.object(installer.node, 'runtime_ready', lambda binary: None)]
        for item in self.patches:
            item.start()
            self.addCleanup(item.stop)

    def account(self, name):
        self.assertEqual(name, 'penhoon-master')
        return types.SimpleNamespace(pw_uid=42424, pw_gid=42424)

    def run_command(self, *arguments, **options):
        self.commands.append(arguments)
        output = b''
        if arguments[0] == 'systemctl':
            self.assertNotIn('p-ui.service', arguments)
            self.assertTrue(all(value.startswith('penhoon-') or value in ('daemon-reload', 'enable', '--now', 'is-active', '--quiet', 'restart', 'systemctl') for value in arguments))
        else:
            self.assertEqual(arguments[:2], ('runuser', '-u'))
            if arguments[2] == 'penhoon-agent':
                self.assertNotIn('environment', options)
                self.assertEqual(arguments[5], 'state')
                output = (pathlib.Path('/var/lib/penhoon-agent') / 'public.json').read_bytes()
                return subprocess.CompletedProcess(arguments, 0, output)
            self.assertEqual(arguments[2], 'penhoon-master')
            self.assertIn('PUI_DB_DSN', options['environment'])
            command = arguments[5:]
            if command[:2] == ('fleet', 'init'):
                installer.CONTROL_FILE.write_text(json.dumps(installer.host_configuration(self.options)))
            elif command[:2] == ('fleet', 'enroll'):
                request = json.loads(options['input'])
                self.assertTrue((installer.BOOTSTRAP / 'local-node-request.json').exists())
                self.enroll_requests.append(request)
                if self.fail_response:
                    self.fail_response = False
                    raise subprocess.CalledProcessError(1, arguments)
                intent = request['enrollment']
                package = {'configuration': {'identity': {'ControlPlaneID': 'f155a487-421f-42a5-a1e0-c2cf346e1f67', 'NodeID': intent['NodeID'], 'InstallationID': intent['InstallationID'], 'Serial': ''},
                    'controlAddress': self.options.control_address, 'enrollmentURL': self.options.enrollment_url, 'serverName': self.options.server_name, 'trustBundlePEM': 'pinned-ca'},
                    'ticket': intent['Ticket'], 'expiresUnixMilli': intent['ExpiresUnixMilli']}
                output = json.dumps(package).encode()
            else:
                self.assertEqual(command, ('preflight',))
        return subprocess.CompletedProcess(arguments, 0, output)

    def install_node(self, options):
        self.assertEqual(options.release_dir, '/signed/release')
        self.assertEqual(options.tuf_root, '/trusted/root')
        self.assertNotIn('PUI_DB_DSN', installer.node.ENVIRONMENT)
        package = installer.protected_json(pathlib.Path(options.enrollment), True)
        self.node_installs.append(package)
        config = package['configuration']
        self.assertEqual(config['controlAddress'], '127.0.0.1:7443')
        self.assertEqual(config['enrollmentURL'], 'https://127.0.0.1:7444/enroll')
        self.assertEqual(config['serverName'], 'control.example')
        self.assertEqual(config['trustBundlePEM'], 'pinned-ca')
        state = pathlib.Path('/var/lib/penhoon-agent')
        state.mkdir(exist_ok=True)
        if not (state / 'credentials.db').exists():
            self.enrollments += 1
            (state / 'credentials.db').write_bytes(b'identity journal')
            (state / 'accounting.db').write_bytes(b'authority journal')
        (state / 'public.json').write_text(json.dumps({'identity': config['identity'], 'enrolled': True}))
        current = pathlib.Path('/usr/local/lib/penhoon/current')
        current.mkdir(parents=True, exist_ok=True)
        (current / 'penhoon-agent').write_text('fixture agent')
        progress = {'phase': 'configured' if self.fail_node else 'active', 'identity': config['identity']}
        installer.node.write_file(current.parent / 'install-progress.json', json.dumps(progress).encode(), 0o600)
        if self.fail_node:
            self.fail_node = False
            raise subprocess.CalledProcessError(1, ['fixture-runtime-start'])

    def test_master_never_constructs_node_or_host_network(self):
        self.options.control_listen = ':443'
        with self.assertRaisesRegex(ValueError, 'unprivileged'):
            installer.install(self.options)
        self.assertEqual(self.commands, [])
        self.options.control_listen = ':7443'
        arguments = ['install.py', '--role', 'master', '--panel-binary', self.options.panel_binary,
                     '--panel-sha256', self.options.panel_sha256, '--database-dsn-file', self.options.database_dsn_file,
                     '--control-address', self.options.control_address, '--enrollment-url', self.options.enrollment_url,
                     '--server-name', self.options.server_name]
        with patch('sys.argv', arguments):
            installer.main()
        self.assertEqual(stat.S_IMODE(pathlib.Path('/run/penhoon-installer/role.lock').stat().st_mode), 0o600)
        self.assertEqual(self.node_installs, [])
        self.assertEqual(self.enroll_requests, [])
        self.assertFalse(pathlib.Path('/var/lib/penhoon-agent').exists())
        values = installer.load_environment('master')
        self.assertEqual(values['PUI_ROLE'], 'master')
        self.assertNotIn('PUI_LOCAL_FLEET_NODE_ID', values)
        unit = pathlib.Path('/etc/systemd/system/penhoon-master.service').read_text()
        self.assertIn('User=penhoon-master\n', unit)
        self.assertIn('CapabilityBoundingSet=\n', unit)
        self.assertIn('ProtectKernelTunables=true\n', unit)
        self.assertNotIn('CAP_NET_ADMIN', unit)
        before = len(self.commands)
        self.options.role = 'combined'
        with self.assertRaisesRegex(ValueError, 'role conversion'):
            installer.install(self.options)
        self.assertEqual(len(self.commands), before)
        record = installer.protected_json(installer.ROLE_FILE)
        record['version'] = 2
        installer.save_role(record)
        saved = installer.ROLE_FILE.read_bytes()
        self.options.role = 'master'
        with self.assertRaisesRegex(ValueError, 'unsupported installation role format'):
            installer.install(self.options)
        with self.assertRaisesRegex(ValueError, 'unsupported installation role format'):
            installer.node.guard_role()
        self.assertEqual(installer.ROLE_FILE.read_bytes(), saved)
        self.assertEqual(len(self.commands), before)

    def test_combined_retries_keep_identity_and_private_bootstrap(self):
        self.options.role = 'combined'
        self.fail_response = True
        with self.assertRaises(subprocess.CalledProcessError):
            installer.install(self.options)
        request = installer.BOOTSTRAP / 'local-node-request.json'
        first = request.read_bytes()
        self.assertEqual(stat.S_IMODE(request.stat().st_mode), 0o600)
        self.assertEqual(request.stat().st_uid, 0)
        self.fail_node = True
        with self.assertRaises(subprocess.CalledProcessError):
            installer.install(self.options)
        package = installer.BOOTSTRAP / 'local-node-enrollment.json'
        self.assertEqual(stat.S_IMODE(package.stat().st_mode), 0o600)
        self.assertEqual(package.stat().st_uid, 0)
        self.assertEqual(request.read_bytes(), first)
        installer.install(self.options)
        self.assertEqual(self.enroll_requests[0], self.enroll_requests[1])
        self.assertEqual(len(self.node_installs), 2)
        self.assertEqual(self.node_installs[0], self.node_installs[1])
        self.assertEqual(self.enrollments, 1)
        self.assertFalse(request.exists())
        self.assertFalse(package.exists())
        identity = self.node_installs[0]['configuration']['identity']
        self.assertEqual(installer.load_environment('combined')['PUI_LOCAL_FLEET_NODE_ID'], identity['NodeID'])
        installer.install(self.options)
        self.assertEqual(len(self.node_installs), 2)
        self.assertEqual(pathlib.Path('/var/lib/penhoon-agent/accounting.db').read_bytes(), b'authority journal')
        self.assertEqual(pathlib.Path('/var/lib/penhoon-agent/credentials.db').read_bytes(), b'identity journal')

    def test_node_role_never_reads_panel_database_or_starts_panel(self):
        self.options.role = 'node'
        self.options.database_dsn_file = '/does-not-exist/private-master-dsn'
        self.options.panel_binary = '/does-not-exist/panel'
        package = self.work / 'node-enrollment.json'
        package.write_text(json.dumps({'configuration': {'identity': {'ControlPlaneID': 'test-plane', 'NodeID': 'test-node', 'InstallationID': 'test-install', 'Serial': ''},
            'controlAddress': '127.0.0.1:7443', 'enrollmentURL': 'https://127.0.0.1:7444/enroll', 'serverName': 'control.example', 'trustBundlePEM': 'pinned-ca'},
            'ticket': 'private-ticket'}))
        package.chmod(0o600)
        self.options.enrollment = str(package)
        installer.install(self.options)
        self.assertEqual(len(self.node_installs), 1)
        self.assertEqual(self.commands, [])
        self.assertFalse(installer.ENV_FILE.exists())
        self.assertFalse(installer.BASE.exists())
        self.assertFalse(pathlib.Path('/etc/systemd/system/penhoon-master.service').exists())
        self.assertEqual(installer.protected_json(installer.ROLE_FILE)['role'], 'node')

    def test_private_inputs_reject_links_and_shared_permissions(self):
        source = pathlib.Path(self.options.database_dsn_file)
        source.chmod(0o644)
        with self.assertRaisesRegex(ValueError, 'protected'):
            installer.install(self.options)
        source.chmod(0o600)
        linked = self.work / 'linked-dsn'
        linked.symlink_to(source)
        self.options.database_dsn_file = str(linked)
        with self.assertRaises(OSError):
            installer.install(self.options)
        linked.unlink()
        os.link(source, linked)
        with self.assertRaisesRegex(ValueError, 'single-link'):
            installer.install(self.options)
        self.assertEqual(self.commands, [])

    def test_ordinary_installation_refused_before_mutation(self):
        pathlib.Path('/etc/p-ui').mkdir()
        with self.assertRaisesRegex(ValueError, 'ordinary panel'):
            installer.install(self.options)
        self.assertEqual(self.commands, [])
        self.assertFalse(installer.ROLE_FILE.exists())


unittest.main()
