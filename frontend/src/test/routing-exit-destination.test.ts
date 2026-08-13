import { describe, it, expect } from 'vitest';

import {
  exitTagFor,
  exitIdFromTag,
  intentToRule,
  ruleToIntent,
  type RoutingRuleRecord,
} from '@/schemas/api/routing';

const NO_ID = () => undefined;

function record(over: Partial<RoutingRuleRecord> = {}): RoutingRuleRecord {
  return {
    id: 1,
    sortIndex: 0,
    enable: true,
    remark: '',
    ingressScope: 'all',
    ingressIds: '[]',
    destKind: 'outbound',
    destTag: '',
    destExitId: null,
    criteria: '{}',
    inspect: false,
    ...over,
  };
}

/*
The picker carries the choice as a tag because that is the control the editor
already has. What must NOT happen is that tag reaching the panel: an uplink is
stored as an id, and a rule naming "exit:4" as an outbound would compile to a
tag no Xray config contains and match nothing.
*/
describe('choosing an uplink in the destination picker', () => {
  it('is stored as an exit id, not as an outbound tag', () => {
    const payload = ruleToIntent({ outboundTag: exitTagFor(4) }, NO_ID);

    expect(payload.destKind).toBe('exit');
    expect(payload.destExitId).toBe(4);
    expect(payload.destTag).toBe('');
  });

  it('comes back out as the same choice when the rule is reopened', () => {
    const shape = intentToRule(record({ destKind: 'exit', destExitId: 4 }), NO_ID);

    expect(shape.outboundTag).toBe(exitTagFor(4));
    expect(exitIdFromTag(shape.outboundTag)).toBe(4);
  });

  /* An ordinary outbound must be untouched by any of this: the prefix exists so
     the two can share one control, not so one can shadow the other. */
  it('leaves an ordinary outbound alone', () => {
    const payload = ruleToIntent({ outboundTag: 'warp' }, NO_ID);

    expect(payload.destKind).toBe('outbound');
    expect(payload.destTag).toBe('warp');
    expect(payload.destExitId).toBeNull();
  });

  it('leaves a balancer alone', () => {
    const payload = ruleToIntent({ balancerTag: 'lb' }, NO_ID);

    expect(payload.destKind).toBe('balancer');
    expect(payload.destTag).toBe('lb');
    expect(payload.destExitId).toBeNull();
  });
});

/* A tag an operator could plausibly name themselves must not be mistaken for an
   uplink. Only the exact prefix followed by a positive integer counts. */
describe('what counts as an uplink tag', () => {
  it.each([
    ['exit:0', null],
    ['exit:-1', null],
    ['exit:abc', null],
    ['exit:', null],
    ['exits:4', null],
    ['my-exit:4', null],
    ['exit:4', 4],
    ['exit:12', 12],
  ])('%s -> %s', (tag, want) => {
    expect(exitIdFromTag(tag)).toBe(want);
  });

  it('ignores an absent destination', () => {
    expect(exitIdFromTag(undefined)).toBeNull();
  });
});
