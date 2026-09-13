import type { CoreStatusView } from '@/generated/zod';
import type { IconName } from '@/components/ui/Icon';
import { coreRuntime, stateTone } from './status';

const ICONS: Record<string, IconName> = { xray: 'xray', singbox: 'nodes', wgkernel: 'shield', awgkernel: 'shield', openvpn: 'lock', ocserv: 'hosts', mtproto: 'link', ssh: 'api' };
const COLORS: Record<string, string> = { xray: '#4c8dff', singbox: '#9254de', wgkernel: '#5ac8b0', awgkernel: '#c79a4b', openvpn: '#5ac8b0', ocserv: '#4c8dff', mtproto: '#9254de', ssh: '#8c8f99' };

export const coreIcon = (id: string): IconName => ICONS[id] ?? 'config';
export const coreColor = (id: string): string => COLORS[id] ?? '#8c8f99';
export const isKernelCore = (id: string): boolean => id === 'wgkernel' || id === 'awgkernel';
export const isBuiltinCore = (core: CoreStatusView): boolean => core.id === 'ssh';

export function coreState(core: CoreStatusView): string {
  return coreRuntime(core).state;
}

export function coreStateTone(core: CoreStatusView) {
  const runtime = coreRuntime(core);
  return runtime.source === 'host' ? { tone: 'warning' as const, labelKey: 'coreRuntime.runningUnverified' } : stateTone(runtime.state, runtime.healthy);
}

export function coreHostProcesses(core: CoreStatusView) {
  const managed = new Set([core.pid, ...core.instances.map((instance) => instance.pid)].filter((pid) => pid !== undefined && pid > 0));
  // A relay or a worker is a child of a managed daemon, and restarts with it.
  const isManaged = (process: { pid: number; parentPid?: number }) => managed.has(process.pid) || (process.parentPid !== undefined && managed.has(process.parentPid));
  return isBuiltinCore(core) ? [] : (core.hostRuntime?.processes ?? []).map((process) => ({ ...process, managed: isManaged(process) }));
}

export function coreHasOtherProcesses(core: CoreStatusView): boolean { return coreHostProcesses(core).some((process) => !process.managed); }
export function coreNeedsRestart(core: CoreStatusView): boolean {
  return coreHostProcesses(core).some((process) => process.managed && process.binaryReplaced && process.state === 'running');
}

export function coreHasManagedRuntime(core: CoreStatusView): boolean {
  // The panel converges Xray itself: a failed start is exactly when Restart is needed.
  return !!core.pid || core.instances.length > 0 || core.id === 'xray';
}

export function coreCanRestart(core: CoreStatusView): boolean { return core.available && coreHasManagedRuntime(core); }
export function coreCanReadLogs(core: CoreStatusView): boolean { return core.logs && coreHasManagedRuntime(core); }
export function coreVersionContextKey(core: CoreStatusView): string {
  return isBuiltinCore(core) ? 'coreRuntime.sshBuild' : coreHasOtherProcesses(core) ? 'coreRuntime.panelBinaryVersion' : 'coreRuntime.installedBinaryVersion';
}

export function coreVersionLabel(core: CoreStatusView): { text?: string; key?: string } {
  if (core.version.installed) return { text: core.version.installed };
  if (isBuiltinCore(core)) return { key: 'pages.cores.builtIn' };
  return { key: core.version.error ? 'pages.cores.versionUnavailable' : 'pages.cores.versionNotReported' };
}

export function coreMaintenanceKey(core: CoreStatusView): string {
  if (isBuiltinCore(core)) return 'pages.cores.builtInMaintenance';
  if (core.version.manageable) return 'pages.cores.panelManaged';
  return isKernelCore(core.id) ? 'pages.cores.kernelMaintenance' : 'pages.cores.reportOnly';
}

export function coreInstanceLabel(core: CoreStatusView): string {
  if (isKernelCore(core.id)) return 'pages.cores.interfaces';
  if (isBuiltinCore(core)) return 'pages.cores.listeners';
  return 'pages.cores.columns.instances';
}

export function coreInstanceSummary(core: CoreStatusView): { labelKey: string; value?: number; valueKey?: string } {
  if (core.instances.length === 0 && (core.pid || core.id === 'xray')) {
    return core.pid ? { labelKey: 'pages.cores.processId', value: core.pid } : { labelKey: 'pages.cores.runtime', valueKey: 'pages.cores.coreWide' };
  }
  return { labelKey: coreInstanceLabel(core), value: core.instances.length };
}

export function coreRestarts(core: CoreStatusView): number | undefined {
  if (isKernelCore(core.id) || isBuiltinCore(core)) return undefined;
  if (core.restarts !== undefined) return core.restarts;
  if (!core.instances.some((instance) => instance.restarts !== undefined)) return undefined;
  return core.instances.reduce((n, instance) => n + (instance.restarts ?? 0), 0);
}

const CORE_ORDER = ['xray', 'singbox', 'wgkernel', 'awgkernel', 'ssh', 'mtproto', 'openvpn', 'ocserv'];
export function orderCores<T extends { id: string }>(cores: readonly T[]): T[] {
  const rank = (id: string) => CORE_ORDER.includes(id) ? CORE_ORDER.indexOf(id) : CORE_ORDER.length;
  return [...cores].sort((a, b) => rank(a.id) - rank(b.id));
}
