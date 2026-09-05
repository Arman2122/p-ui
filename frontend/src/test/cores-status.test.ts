import { describe, expect, it } from 'vitest';

import { sameVersion, stateTone, summarizeCores } from '@/lib/cores/status';
import type { CoreStatusView } from '@/generated/zod';

function core(over: Partial<CoreStatusView>): CoreStatusView {
  return {
    id: 'x', titleKey: 'cores.x.title', available: true, state: 'running', ready: true,
    instances: [], version: { reported: false, manageable: false }, logs: false,
    ...over,
  };
}

/* The summary is what an operator reads first, so its buckets are pinned: a
   running daemon that does not answer needs attention, a stopped one is
   neither healthy nor a problem, and a core the host cannot run is set apart. */
describe('summarizeCores', () => {
  it('puts each core in exactly one bucket', () => {
    const s = summarizeCores([
      core({ id: 'a' }),
      core({ id: 'b', state: 'running', ready: false }),
      core({ id: 'c', state: 'degraded', ready: false }),
      core({ id: 'd', state: 'failed', ready: false }),
      core({ id: 'e', state: 'stopped', ready: false }),
      core({ id: 'f', state: 'idle', ready: true }),
      core({ id: 'g', available: false, state: 'unknown', ready: false }),
    ]);
    expect(s).toEqual({ total: 7, healthy: 1, attention: 3, unavailable: 1 });
  });
});

describe('stateTone', () => {
  it('reads running without readiness as a warning, not green', () => {
    expect(stateTone('running', true)).toEqual({ tone: 'success', labelKey: 'pages.cores.state.running' });
    expect(stateTone('running', false)).toEqual({ tone: 'warning', labelKey: 'pages.cores.state.notReady' });
    expect(stateTone('failed', false).tone).toBe('error');
    expect(stateTone('nonsense', true)).toEqual({ tone: 'default', labelKey: 'pages.cores.state.unknown' });
  });
});

describe('sameVersion', () => {
  it('compares a tag with a reported version across the v', () => {
    expect(sameVersion('26.7.28', 'v26.7.28')).toBe(true);
    expect(sameVersion('v1.3.0', 'v1.3.0')).toBe(true);
    expect(sameVersion('1.2.4', 'v1.3.0')).toBe(false);
    expect(sameVersion(undefined, 'v1.3.0')).toBe(false);
  });
});
