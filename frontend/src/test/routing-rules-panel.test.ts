import { describe, it, expect } from 'vitest';

import {
  criteriaFromForm,
  criteriaToForm,
  ingressIdsToArray,
  intentToRule,
  intersectMask,
  ruleToIntent,
  type RoutingRuleRecord,
  type RoutingSubjectView,
} from '@/schemas/api/routing';

function record(over: Partial<RoutingRuleRecord> = {}): RoutingRuleRecord {
  return {
    id: 1, sortIndex: 0, enable: true, remark: '', ingressScope: 'selected',
    ingressIds: '[2]', destKind: 'outbound', destTag: 'warp', destExitId: null,
    criteria: '{}', inspect: false, ...over,
  } as RoutingRuleRecord;
}

const SUBJECTS: RoutingSubjectView[] = [
  { inboundId: 1, tag: 'vless-in', selector: 'internal', routable: true, blockedKey: '', criteriaMask: ['ip', 'port', 'domain', 'user'] },
  { inboundId: 2, tag: 'wg-home', selector: 'device', routable: true, blockedKey: '', criteriaMask: ['ip', 'port'] },
];

/*
The two halves of the list are one sentence in two shapes. These conversions are
what let a single editor open either without knowing which it has, so a round
trip that loses a matcher is the way a rule quietly stops doing its job.
*/
describe('intent and xray rule shapes', () => {
  it('renders an intent rule as the xray rule the editor already speaks', () => {
    const shape = intentToRule(
      record({ criteria: '{"domain":["a.com","b.com"],"port":"443"}' }),
      (id) => (id === 2 ? 'wg-home' : undefined),
    );

    expect(shape.inboundTag).toEqual(['wg-home']);
    expect(shape.domain).toEqual(['a.com', 'b.com']);
    expect(shape.port).toBe('443');
    expect(shape.outboundTag).toBe('warp');
  });

  it('converts an edited rule back to intent, resolving tags to inbound ids', () => {
    const payload = ruleToIntent(
      { enabled: true, inboundTag: ['wg-home'], ip: ['1.1.1.1'], outboundTag: 'warp' },
      (tag) => (tag === 'wg-home' ? 2 : undefined),
    );

    expect(payload.ingressScope).toBe('selected');
    expect(JSON.parse(payload.ingressIds)).toEqual([2]);
    expect(payload.destKind).toBe('outbound');
    expect(payload.destTag).toBe('warp');
    expect(JSON.parse(payload.criteria)).toEqual({ ip: ['1.1.1.1'] });
  });

  /* Naming no inbound means every inbound, which is a scope rather than an
     empty rule: emitting selected-with-nothing would be refused at save. */
  it('reads a rule that names no inbound as the all scope', () => {
    expect(ruleToIntent({ outboundTag: 'warp' }, () => undefined).ingressScope).toBe('all');
  });

  it('survives a round trip without losing a matcher', () => {
    const before = record({ criteria: '{"domain":["x.com"],"ip":["10.0.0.0/8"],"port":"80"}' });
    const shape = intentToRule(before, () => 'wg-home');
    const after = ruleToIntent(shape, () => 2);

    expect(JSON.parse(after.criteria)).toEqual({
      domain: ['x.com'], ip: ['10.0.0.0/8'], port: '80',
    });
  });

  it('keeps a balancer destination a balancer', () => {
    const shape = intentToRule(record({ destKind: 'balancer', destTag: 'lb' }), () => 'wg-home');
    expect(shape.balancerTag).toBe('lb');
    expect(ruleToIntent(shape, () => 2).destKind).toBe('balancer');
  });
});

/*
One rule carries one set of criteria, so the narrowest subject decides what can
be entered. `user` can never match a kernel ingress -- Xray's tun handler builds
a user with no email -- so offering it would rebuild the bug this work removes.
*/
describe('criteria mask', () => {
  it('is the intersection across the selected subjects', () => {
    expect([...intersectMask(SUBJECTS)].sort()).toEqual(['ip', 'port']);
  });

  it(`is the subject own mask when only one is selected`, () => {
    expect([...intersectMask([SUBJECTS[0]])].sort()).toEqual(['domain', 'ip', 'port', 'user']);
  });

  it('offers everything when nothing is selected yet', () => {
    expect(intersectMask([]).has('user')).toBe(true);
  });
});

describe('criteria round trip', () => {
  it('splits a list criterion and drops the empties', () => {
    expect(JSON.parse(criteriaFromForm({ domain: 'a.com, b.com ', port: '443', ip: '' })))
      .toEqual({ domain: ['a.com', 'b.com'], port: '443' });
  });

  it('survives unparsable stored values rather than throwing', () => {
    expect(criteriaToForm('{not json')).toEqual({});
    expect(ingressIdsToArray('{not json')).toEqual([]);
  });
});
