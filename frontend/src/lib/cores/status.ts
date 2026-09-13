import type { CoreStatusView } from '@/generated/zod';

/* How a core's state and readiness read on the page: the pair is one badge,
   because "running" with nothing answering is the case §7 exists for. */
export type StateTone = 'success' | 'warning' | 'error' | 'default';

/* Host observations establish process existence, never managed readiness;
   a detected standalone daemon cannot clear a managed control failure. */
export function coreRuntime(core: CoreStatusView) {
  const host = core.id === 'ssh' ? undefined : core.hostRuntime;
  const hostRunning = !host?.error && host?.state === 'running';
  if (core.state === 'failed' || core.state === 'degraded') return {
    state: core.state, source: 'managed' as const, running: hostRunning || core.instances.some((instance) => instance.state === 'running'),
    healthy: false, descriptionKey: 'coreRuntime.managedFailure',
  };
  if (core.state === 'running') return {
    state: 'running', source: 'managed' as const, running: true, healthy: core.ready,
    descriptionKey: core.ready ? 'coreRuntime.managedHealthy' : 'coreRuntime.managedNotReady',
  };
  if (host?.error || host?.state === 'unknown') return {
    state: 'unknown', source: 'unknown' as const, running: false, healthy: false, descriptionKey: 'coreRuntime.probeUnknown',
  };
  if (hostRunning) return {
    state: 'running', source: 'host' as const, running: true, healthy: false, descriptionKey: 'coreRuntime.hostUnverified',
  };
  if (host?.state === 'stopped' || core.state === 'idle' || core.state === 'stopped') return {
    state: 'stopped', source: 'none' as const, running: false, healthy: false, descriptionKey: 'coreRuntime.stoppedHelp',
  };
  return { state: 'unknown', source: 'unknown' as const, running: false, healthy: false, descriptionKey: 'coreRuntime.probeUnknown' };
}

export function stateTone(state: string, ready: boolean): { tone: StateTone; labelKey: string } {
  switch (state) {
    case 'unavailable':
      return { tone: 'default', labelKey: 'pages.cores.unavailable' };
    case 'running':
      return ready
        ? { tone: 'success', labelKey: 'pages.cores.state.running' }
        : { tone: 'warning', labelKey: 'pages.cores.state.notReady' };
    case 'degraded':
      return { tone: 'warning', labelKey: 'pages.cores.state.degraded' };
    case 'failed':
      return { tone: 'error', labelKey: 'pages.cores.state.failed' };
    case 'stopped':
      return { tone: 'default', labelKey: 'pages.cores.state.stopped' };
    case 'idle':
      return { tone: 'default', labelKey: 'pages.cores.state.idle' };
    default:
      return { tone: 'default', labelKey: 'pages.cores.state.unknown' };
  }
}

export interface CoresSummary {
  total: number;
  running: number;
  healthy: number;
  attention: number;
  unavailable: number;
}

/* Running and healthy overlap; installation availability is counted separately. */
export function summarizeCores(cores: CoreStatusView[]): CoresSummary {
  let running = 0;
  let healthy = 0;
  let attention = 0;
  let unavailable = 0;
  for (const c of cores) {
    const runtime = coreRuntime(c);
    if (!c.available) unavailable += 1;
    if (runtime.running) running += 1;
    if (runtime.healthy) healthy += 1;
    else if (runtime.state === 'failed' || runtime.state === 'degraded' || runtime.running || c.hostRuntime?.error) attention += 1;
  }
  return { total: cores.length, running, healthy, attention, unavailable };
}

/* Installed and Available compare by the bare number: a channel lists tags
   ("v26.7.28") and a binary reports itself without the v ("26.7.28"). */
export function sameVersion(installed: string | undefined, tag: string): boolean {
  if (!installed) return false;
  return installed.replace(/^v/, '') === tag.replace(/^v/, '');
}
