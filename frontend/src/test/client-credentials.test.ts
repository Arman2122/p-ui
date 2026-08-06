import { describe, it, expect } from 'vitest';

import { credentialsForKinds } from '@/lib/cores/client-credentials';
import type { CoreView } from '@/generated/zod';

const CORES: CoreView[] = [
  {
    id: 'xray',
    titleKey: 'cores.xray.title',
    kinds: ['vless', 'wireguard', 'tun'],
    caps: {},
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
    clientCredentials: { mtproto: ['secret', 'adTag'] },
  },
];

describe('credentialsForKinds', () => {
  const cases: { name: string; cores: CoreView[] | undefined; kinds: string[]; want: string[] }[] = [
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
          clientCredentials: { future: ['uuid', 'somethingThisBuildCannotRender'] },
        },
      ],
      kinds: ['future'],
      want: ['uuid'],
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
      name: 'no manifest at all still renders an editable form',
      cores: undefined,
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
