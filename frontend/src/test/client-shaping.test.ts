import { describe, it, expect } from 'vitest';

import { shapingForKinds, shapeableKinds } from '@/lib/cores/client-shaping';
import type { CoreView } from '@/generated/zod';

const CORES: CoreView[] = [
  {
    id: 'wireguard',
    titleKey: 'cores.wireguard.title',
    kinds: ['wgkernel'],
    caps: {},
    available: true,
    unavailable: '',
    clientCredentials: {},
    shaping: { wgkernel: 'innerIP' },
  },
  {
    id: 'xray',
    titleKey: 'cores.xray.title',
    kinds: ['vless', 'vmess'],
    caps: {},
    available: true,
    unavailable: '',
    clientCredentials: { vless: ['uuid'] },
    shaping: {},
  },
];

describe('shapingForKinds', () => {
  const cases: { name: string; kinds: string[]; shapeable: string[]; unshapeable: string[] }[] = [
    {
      name: 'a kind whose core declares a kernel key can carry a rate',
      kinds: ['wgkernel'],
      shapeable: ['wgkernel'],
      unshapeable: [],
    },
    {
      name: 'a kind no core declares cannot, and that is an answer rather than an unknown',
      kinds: ['vless'],
      shapeable: [],
      unshapeable: ['vless'],
    },
    {
      name: 'a kind no core in this build serves at all reads as unshapeable',
      kinds: ['ocserv'],
      shapeable: [],
      unshapeable: ['ocserv'],
    },
    {
      name: 'a client on both keeps each side separate, because the rate lands per inbound',
      kinds: ['wgkernel', 'vless'],
      shapeable: ['wgkernel'],
      unshapeable: ['vless'],
    },
    {
      name: 'a client attached to nothing has no kind to ask about',
      kinds: [],
      shapeable: [],
      unshapeable: [],
    },
    {
      name: 'the same kind twice is asked once',
      kinds: ['wgkernel', 'wgkernel'],
      shapeable: ['wgkernel'],
      unshapeable: [],
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      const verdict = shapingForKinds(CORES, c.kinds);
      expect(verdict.shapeable).toEqual(c.shapeable);
      expect(verdict.unshapeable).toEqual(c.unshapeable);
    });
  }

  /* SelectorNone is the empty string on the wire, and an empty selector is a
     core saying it cannot shape — reading the key's presence would invert it. */
  it('an empty selector is not a kernel key', () => {
    const cores: CoreView[] = [{ ...CORES[1], shaping: { vless: '' } }];
    expect(shapingForKinds(cores, ['vless']).unshapeable).toEqual(['vless']);
  });
});

describe('shapeableKinds', () => {
  it('names every kind this build can shape, sorted', () => {
    expect(shapeableKinds(CORES)).toEqual(['wgkernel']);
  });

  it('is empty when no core declares one, so a page can say rates reach nothing here', () => {
    expect(shapeableKinds([CORES[1]])).toEqual([]);
  });
});
