import { describe, expect, it } from 'vitest';
import type { CoreStatusView } from '@/generated/zod';
import { summarizeCores } from '@/lib/cores/status';
import { coreCanReadLogs, coreCanRestart, coreHostProcesses, coreMaintenanceKey, coreState, coreStateTone, coreVersionContextKey, isBuiltinCore } from '@/lib/cores/display';

const core = (overrides: Partial<CoreStatusView> = {}): CoreStatusView => ({ id: 'openvpn', titleKey: 'cores.openvpn.title', available: true, ready: false, state: 'idle', instances: [], logs: true, version: { reported: true, manageable: false, installed: '2.6.12' }, ...overrides });
const host = { state: 'running', processes: [{ pid: 321, executable: '/usr/sbin/openvpn', state: 'running' }] };

describe('actual host core runtime', () => {
  it('shows a detected daemon as running without claiming verified health or requiring panel installation readiness', () => {
    const external = core({ available: false, hostRuntime: host });
    expect(coreState(external)).toBe('running');
    expect(coreStateTone(external)).toMatchObject({ tone: 'warning', labelKey: 'coreRuntime.runningUnverified' });
    expect(coreStateTone(core({ ready: true, hostRuntime: host })).tone).toBe('warning');
  });

  it('keeps installation and loaded availability separate from a stopped runtime', () => {
    expect(coreState(core({ ready: true, hostRuntime: { state: 'stopped', processes: [] } }))).toBe('stopped');
    expect(coreState(core({ available: false }))).toBe('stopped');
  });

  it('keeps managed failure and readiness ahead of externally detected processes', () => {
    expect(coreState(core({ state: 'failed', hostRuntime: host }))).toBe('failed');
    expect(coreStateTone(core({ state: 'degraded', hostRuntime: host })).tone).toBe('warning');
    expect(coreStateTone(core({ state: 'running', ready: false, hostRuntime: host })).labelKey).toBe('pages.cores.state.notReady');
    expect(coreStateTone(core({ state: 'running', ready: true, hostRuntime: { ...host, error: 'proc unreadable' } })).tone).toBe('success');
  });

  it('does not turn a partial failed host probe or an unrelated system sshd into verified runtime', () => {
    expect(coreState(core({ hostRuntime: { ...host, error: 'proc unreadable' } }))).toBe('unknown');
    expect(coreState(core({ id: 'ssh', hostRuntime: host }))).toBe('stopped');
    expect(summarizeCores([core({ hostRuntime: { ...host, error: 'proc unreadable' } })]).healthy).toBe(0);
  });

  it('counts seven actual running cores separately from four verified healthy cores', () => {
    const cores = [
      ...['xray', 'ssh', 'wgkernel', 'awgkernel'].map((id) => core({ id, state: 'running', ready: true })),
      ...['mtproto', 'ocserv', 'openvpn'].map((id) => core({ id, hostRuntime: host })),
      core({ id: 'singbox', hostRuntime: { state: 'stopped', processes: [] } }),
    ];
    expect(summarizeCores(cores)).toMatchObject({ total: 8, running: 7, healthy: 4 });
  });

  it('counts a child of a managed daemon, a relay or a worker, as managed', () => {
    const withChildren = core({ pid: 321, hostRuntime: { ...host, processes: [
      ...host.processes, { pid: 330, parentPid: 321, executable: '/usr/sbin/openvpn', state: 'running' }, { pid: 331, parentPid: 1, executable: '/usr/sbin/openvpn', state: 'running' },
    ] } });
    expect(coreHostProcesses(withChildren).map((process) => [process.pid, process.managed])).toEqual([[321, true], [330, true], [331, false]]);
  });

  it('keeps reported SSH build versions attached to panel maintenance', () => {
    const ssh = core({ id: 'ssh', version: { reported: true, manageable: false, installed: 'Penhoon SSH v1.5.3 (golang.org/x/crypto v0.42.0)' } });
    expect(isBuiltinCore(ssh)).toBe(true);
    expect(coreMaintenanceKey(ssh)).toBe('pages.cores.builtInMaintenance');
  });

  it('keeps host process ownership and partial observations without granting unmanaged controls', () => {
    const external = core({ hostRuntime: host });
    expect(coreCanRestart(external)).toBe(false);
    expect(coreCanReadLogs(external)).toBe(false);
    expect(coreVersionContextKey(external)).toBe('coreRuntime.panelBinaryVersion');
    expect(coreCanRestart(core({ id: 'xray', hostRuntime: host }))).toBe(true);
    const managed = core({ pid: 321, instances: [{ instanceId: 7, tag: 'managed', state: 'stopped', ready: false, pid: 322 }], hostRuntime: { ...host, error: 'partial probe', processes: [
      ...host.processes, { pid: 322, executable: '/panel/openvpn', state: 'stopped' }, { pid: 323, executable: '/other/openvpn', state: 'running' },
    ] } });
    expect(coreHostProcesses(managed).map((process) => [process.pid, process.managed])).toEqual([[321, true], [322, true], [323, false]]);
    expect(coreCanRestart(managed)).toBe(true);
    expect(coreCanReadLogs(managed)).toBe(true);
  });
});
