import { describe, expect, it } from 'vitest';

import { kindsWithoutIpLimit } from '@/lib/cores/ip-limit';
import type { CoreView } from '@/generated/zod';

/*
An IP limit on a protocol that cannot report where a connection came from does
nothing, and the form accepted one anyway.

The cap is decided from live sessions: EvaluateIPLimits iterates only the
clients a core reported a session for, with the source address. mtg reports a
secret in use and no address at all, so its core implements no SessionReporter,
bind leaves Bound.Sessions nil, and an MTProto client never enters that set. The
number was stored, shown, and enforced on nobody.

Asked of each core's own sessionKinds rather than a protocol list here, so a
core added later declares for itself. F120 / F302.
*/

const base: CoreView = {
  id: 'xray',
  titleKey: 'cores.xray.title',
  kinds: ['vless'],
  caps: {},
  available: true,
  unavailable: '',
  exitKinds: [],
  clientCredentials: {},
  shaping: {},
  sessionKinds: ['vless'],
  rateKinds: [],
  applyDelaySeconds: {},
  portlessKinds: [],
};

const MTPROTO: CoreView = {
  ...base,
  id: 'mtproto',
  titleKey: 'cores.mtproto.title',
  kinds: ['mtproto'],
  // The point of the whole thing: it serves a kind and reports no sessions.
  sessionKinds: [],
  rateKinds: [],
  applyDelaySeconds: {},
  portlessKinds: [],
};

describe('which of a client’s inbounds an IP limit can bite on', () => {
  it('names the kind whose core reports no sessions', () => {
    expect(kindsWithoutIpLimit([base, MTPROTO], ['mtproto'])).toEqual(['mtproto']);
  });

  it('says nothing about a kind whose core does report them', () => {
    expect(kindsWithoutIpLimit([base, MTPROTO], ['vless'])).toEqual([]);
  });

  it('names only the half that cannot, for a client on both', () => {
    expect(kindsWithoutIpLimit([base, MTPROTO], ['vless', 'mtproto'])).toEqual(['mtproto']);
  });

  it('does not repeat a kind a client is attached to twice', () => {
    expect(kindsWithoutIpLimit([base, MTPROTO], ['mtproto', 'mtproto'])).toEqual(['mtproto']);
  });

  /* A kind NO core claims is a quarantined or unknown inbound. Refusing its cap
     would be the frontend guessing on a core's behalf. */
  it('leaves a kind no core claims alone', () => {
    expect(kindsWithoutIpLimit([base, MTPROTO], ['something-new'])).toEqual([]);
  });

  it('says nothing before the core list has loaded', () => {
    expect(kindsWithoutIpLimit(undefined, ['mtproto'])).toEqual([]);
    expect(kindsWithoutIpLimit([base, MTPROTO], [])).toEqual([]);
  });
});
