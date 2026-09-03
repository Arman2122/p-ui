import { describe, it, expect } from 'vitest';

import { kindBindsNoPort } from '@/lib/cores/port';
import type { CoreView } from '@/generated/zod';

/*
Whether an inbound binds a port is the core's answer, not a protocol list here.

An Xray TUN device is a kernel interface, so the panel demanded a port for it,
counted the stored value as occupied and refused a real inbound there. F319.
*/
const CORES = [
  {
    id: 'xray',
    titleKey: 'cores.xray.title',
    kinds: ['vless', 'tun'],
    exitKinds: [],
    caps: {},
    clientCredentials: {},
    shaping: {},
    sessionKinds: [],
    rateKinds: [],
    applyDelaySeconds: {},
    portlessKinds: ['tun'],
    available: true,
    unavailable: '',
  },
] as unknown as CoreView[];

describe('which kinds bind no port', () => {
  it('answers for the kind a core declared portless', () => {
    expect(kindBindsNoPort(CORES, 'tun')).toBe(true);
  });

  it('leaves every other kind of the same core binding a port', () => {
    expect(kindBindsNoPort(CORES, 'vless')).toBe(false);
  });

  /* A kind no core claims is a quarantined or unknown inbound; treating it as
     portless would drop it out of conflict detection on a guess. */
  it('treats an unclaimed kind as binding a port', () => {
    expect(kindBindsNoPort(CORES, 'something-else')).toBe(false);
  });

  it('answers false rather than throwing before the manifest arrives', () => {
    expect(kindBindsNoPort(undefined, 'tun')).toBe(false);
  });
});
