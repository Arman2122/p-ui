import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { dialerProxyCycleTags } from '@/pages/xray/outbounds/outbounds-tab-helpers';

/*
The dialerProxy picker offered a tag that closes the loop.

dialerProxy makes an outbound dial THROUGH another one. The only exclusion was
the row being edited, which stops A→A and nothing longer: with B already
dialling through A, the picker still offered B as A's dialerProxy, and choosing
it left A→B→A. xray follows that until the dial gives up, so both outbounds
are dead and nothing anywhere says why -- the config is valid, the core starts,
the traffic goes nowhere.

Nothing on the Go side checks it either, so the picker is where it has to be
caught. Every tag whose chain reaches the outbound being edited is excluded, at
any depth. F082.
*/

const chain = (tag: string, via?: string) => ({
  tag,
  streamSettings: via ? { sockopt: { dialerProxy: via } } : undefined,
});

describe('choosing a dialerProxy', () => {
  it('excludes a tag that already dials through this one', () => {
    const unsafe = dialerProxyCycleTags([chain('A'), chain('B', 'A'), chain('C')], 'A');

    expect(unsafe.has('B'), 'B dials through A, so A dialling through B is a loop with no bottom').toBe(true);
    expect(unsafe.has('C'), 'C is unrelated and must stay selectable').toBe(false);
  });

  it('follows the chain further than one step', () => {
    const unsafe = dialerProxyCycleTags(
      [chain('A'), chain('B', 'A'), chain('C', 'B'), chain('D', 'C')],
      'A',
    );

    expect([...unsafe].sort()).toEqual(['A', 'B', 'C', 'D']);
  });

  it('terminates on a loop that does not involve the edited outbound', () => {
    const unsafe = dialerProxyCycleTags([chain('A'), chain('B', 'C'), chain('C', 'B')], 'A');

    expect(unsafe.has('B')).toBe(false);
    expect(unsafe.has('C')).toBe(false);
  });

  it('offers everything when the outbound has no tag yet', () => {
    expect(dialerProxyCycleTags([chain('A'), chain('B', 'A')], '').size).toBe(0);
  });
});

// And the picker really applies it — a helper nothing calls fixes nothing.
describe('the outbounds tab', () => {
  const source = readFileSync(
    resolve(__dirname, '../pages/xray/outbounds/OutboundsTab.tsx'),
    'utf8',
  );

  it('filters the dialerProxy options through it', () => {
    const at = source.indexOf('const dialerProxyTags');
    expect(at, 'dialerProxyTags was renamed; this guard is now vacuous').toBeGreaterThan(-1);
    const body = source.slice(at, at + 900);
    expect(body).toContain('dialerProxyCycleTags(');
    expect(body).toContain('!cycles.has(o.tag)');
  });
});
