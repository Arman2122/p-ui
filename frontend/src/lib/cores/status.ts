import type { CoreStatusView } from '@/generated/zod';

/* How a core's state and readiness read on the page: the pair is one badge,
   because "running" with nothing answering is the case §7 exists for. */
export type StateTone = 'success' | 'warning' | 'error' | 'default';

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
  healthy: number;
  attention: number;
  unavailable: number;
}

/* Stopped and idle cores are neutral; failures and running cores without readiness need attention. */
export function summarizeCores(cores: CoreStatusView[]): CoresSummary {
  let healthy = 0;
  let attention = 0;
  let unavailable = 0;
  for (const c of cores) {
    if (!c.available) {
      unavailable += 1;
      continue;
    }
    if (c.state === 'running' && c.ready) healthy += 1;
    else if (c.state === 'failed' || c.state === 'degraded' || c.state === 'running') attention += 1;
  }
  return { total: cores.length, healthy, attention, unavailable };
}

/* Installed and Available compare by the bare number: a channel lists tags
   ("v26.7.28") and a binary reports itself without the v ("26.7.28"). */
export function sameVersion(installed: string | undefined, tag: string): boolean {
  if (!installed) return false;
  return installed.replace(/^v/, '') === tag.replace(/^v/, '');
}
