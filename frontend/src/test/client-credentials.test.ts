import { describe, it, expect } from 'vitest';

import { credentialsForKinds } from '@/lib/cores/client-credentials';
import type { CoreView } from '@/generated/zod';

const CORES: CoreView[] = [
  {
    id: 'xray',
    titleKey: 'cores.xray.title',
    kinds: ['vless', 'wireguard', 'tun'],
    caps: {},
    available: true,
    unavailable: '',
    shaping: {},
    exitKinds: [],
    clientCredentials: {
      vless: ['uuid'],
      wireguard: ['privateKey', 'publicKey', 'preSharedKey', 'allowedIPs'],
    },
  },
  {
    id: 'mtproto',
    titleKey: 'cores.mtproto.title',
    kinds: ['mtproto'],
    caps: {},
    available: true,
    unavailable: '',
    shaping: {},
    exitKinds: [],
    clientCredentials: { mtproto: ['secret', 'adTag'] },
  },
];

describe('credentialsForKinds', () => {
  const cases: { name: string; cores: CoreView[]; kinds: string[]; want: string[] }[] = [
    {
      name: 'a declared kind gets exactly what its core declares',
      cores: CORES,
      kinds: ['vless'],
      want: ['uuid'],
    },
    {
      name: 'a client on two kinds gets the union, so neither inbound loses a field',
      cores: CORES,
      kinds: ['vless', 'mtproto'],
      want: ['adTag', 'secret', 'uuid'],
    },
    {
      name: 'wireguard gets all four of its declared keys, not a subset',
      cores: CORES,
      kinds: ['wireguard'],
      want: ['allowedIPs', 'preSharedKey', 'privateKey', 'publicKey'],
    },
    {
      name: 'a name outside the vocabulary is dropped rather than rendered blank',
      cores: [
        {
          id: 'future',
          titleKey: 'cores.future.title',
          kinds: ['future'],
          caps: {},
          available: true,
          unavailable: '',
          shaping: {},
          exitKinds: [],
          clientCredentials: { future: ['uuid', 'somethingThisBuildCannotRender'] },
        },
      ],
      kinds: ['future'],
      want: ['uuid'],
    },
    {
      /* Preflight can mark a core unavailable on a host whose kernel lacks the
         module. Its stored clients must stay editable, keys and all. */
      name: 'an unavailable core still declares, so its stored clients stay editable',
      cores: [
        {
          id: 'wgkernel',
          titleKey: 'cores.wgkernel.title',
          kinds: ['wgkernel'],
          caps: {},
          available: false,
          unavailable: 'kernel WireGuard is not available on this host',
          shaping: {},
          exitKinds: [],
          clientCredentials: { wgkernel: ['privateKey', 'publicKey', 'preSharedKey', 'allowedIPs'] },
        },
      ],
      kinds: ['wgkernel'],
      want: ['allowedIPs', 'preSharedKey', 'privateKey', 'publicKey'],
    },
    {
      name: 'a kind no core declares keeps what the form has always shown',
      cores: CORES,
      kinds: ['quarantined'],
      want: ['auth', 'password', 'uuid'],
    },
    {
      name: 'a kind its own core skips falls back rather than rendering nothing',
      cores: CORES,
      kinds: ['tun'],
      want: ['auth', 'password', 'uuid'],
    },
    {
      name: 'a client attached to no inbound keeps what the form has always shown',
      cores: CORES,
      kinds: [],
      want: ['auth', 'password', 'uuid'],
    },
    {
      name: 'an empty manifest falls back per kind rather than rendering nothing',
      cores: [],
      kinds: ['wireguard'],
      want: ['auth', 'password', 'uuid'],
    },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect([...credentialsForKinds(c.cores, c.kinds)].sort()).toEqual(c.want);
    });
  }
});
