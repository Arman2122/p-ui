import { describe, it, expect } from 'vitest';

import { unavailableKinds } from '@/lib/cores/core-availability';
import type { CoreView } from '@/generated/zod';

function core(over: Partial<CoreView>): CoreView {
  return {
    id: 'x',
    titleKey: 'cores.x.title',
    kinds: [],
    caps: {},
    clientCredentials: {},
    shaping: {},
    available: true,
    unavailable: '',
    ...over,
  };
}

const XRAY = core({ id: 'xray', kinds: ['vless', 'wireguard'] });
const WGKERNEL = core({
  id: 'wgkernel',
  kinds: ['wgkernel'],
  available: false,
  unavailable: 'wireguard: /sys/module/wireguard is absent',
});

describe('unavailableKinds', () => {
  const cases: { name: string; cores: CoreView[] | undefined; want: [string, string][] }[] = [
    {
      name: 'a core that fails Preflight blocks every kind it owns, with its own reason',
      cores: [XRAY, WGKERNEL],
      want: [['wgkernel', 'wireguard: /sys/module/wireguard is absent']],
    },
    {
      name: 'a host that can run every core blocks nothing',
      cores: [XRAY, core({ id: 'wgkernel', kinds: ['wgkernel'] })],
      want: [],
    },
    {
      name: 'a multi-kind core takes all of its kinds down together',
      cores: [core({ id: 'multi', kinds: ['a', 'b'], available: false, unavailable: 'no' })],
      want: [
        ['a', 'no'],
        ['b', 'no'],
      ],
    },
    {
      name: 'no manifest offers everything, so a failed fetch never locks the operator out',
      cores: undefined,
      want: [],
    },
    {
      name: 'an empty manifest offers everything for the same reason',
      cores: [],
      want: [],
    },
  ];

  for (const tc of cases) {
    it(tc.name, () => {
      expect([...unavailableKinds(tc.cores).entries()]).toEqual(tc.want);
    });
  }
});
