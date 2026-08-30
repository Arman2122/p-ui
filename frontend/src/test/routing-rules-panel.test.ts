import { describe, it, expect } from 'vitest';

import {
  criteriaFromForm,
  criteriaToForm,
  ingressIdsToArray,
  intentToRule,
  intersectMask,
  recordToPayload,
  ruleToIntent,
  type RoutingRuleRecord,
  type RoutingSubjectView,
} from '@/schemas/api/routing';
import { expectResolved } from './routing-intent-helpers';

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
    const payload = expectResolved(ruleToIntent(
      { enabled: true, inboundTag: ['wg-home'], ip: ['1.1.1.1'], outboundTag: 'warp' },
      (tag) => (tag === 'wg-home' ? 2 : undefined),
    ));

    expect(payload.ingressScope).toBe('selected');
    expect(JSON.parse(payload.ingressIds)).toEqual([2]);
    expect(payload.destKind).toBe('outbound');
    expect(payload.destTag).toBe('warp');
    expect(JSON.parse(payload.criteria)).toEqual({ ip: ['1.1.1.1'] });
  });

  /* Naming no inbound means every inbound, which is a scope rather than an
     empty rule: emitting selected-with-nothing would be refused at save. */
  it('reads a rule that names no inbound as the all scope', () => {
    expect(expectResolved(ruleToIntent({ outboundTag: 'warp' }, () => undefined)).ingressScope).toBe('all');
  });

  /*
    And its opposite, which used to be indistinguishable from it.

    A rule that NAMES an inbound whose tag no longer resolves is not a rule that
    names none. Subjects() omits a disabled inbound, PruneInbound leaves a
    deleted one's rule naming nothing, and the subjects query is silent with an
    empty fallback while it is in flight — so all three came back as scope 'all'
    and were persisted by the very next toggle. A rule written for one customer
    started governing every client on the box.
  */
  it('refuses a rule whose named inbound cannot be resolved', () => {
    const shape = intentToRule(record({ ingressIds: '[3]' }), () => undefined);
    expect(shape.inboundTag).toEqual(['#3']);

    const result = ruleToIntent(shape, () => undefined);

    expect(result.ok).toBe(false);
    expect(result.ok === false && result.unresolved).toEqual(['#3']);
  });

  // Narrowing silently is the same defect pointing the other way.
  it('refuses when only some of the named inbounds resolve', () => {
    const result = ruleToIntent(
      { inboundTag: ['wg-home', 'gone'], outboundTag: 'warp' },
      (tag) => (tag === 'wg-home' ? 2 : undefined),
    );

    expect(result.ok).toBe(false);
    expect(result.ok === false && result.unresolved).toEqual(['gone']);
  });

  /*
    A toggle must keep working for exactly the rules the refusal protects.

    Once an unresolvable tag is refused, a toggle routed through the editor's
    shape would refuse to flip a rule whose inbound is disabled — the right
    answer for an edit, the wrong one for changing one flag. Reading the stored
    row keeps the scope the operator set and never consults the map at all.
  */
  it('toggles a rule whose inbound cannot be resolved, without touching its scope', () => {
    const stored = record({ ingressIds: '[3]', criteria: '{"domain":["x.com"]}', destTag: 'warp' });

    // The round trip cannot serve this rule any more, which is the whole point.
    expect(ruleToIntent(intentToRule(stored, () => undefined), () => undefined).ok).toBe(false);

    const payload = { ...recordToPayload(stored), enable: false };

    expect(payload.ingressScope).toBe('selected');
    expect(payload.ingressIds).toBe('[3]');
    expect(JSON.parse(payload.criteria)).toEqual({ domain: ['x.com'] });
    expect(payload.destTag).toBe('warp');
    expect(payload.enable).toBe(false);
  });

  it('survives a round trip without losing a matcher', () => {
    const before = record({ criteria: '{"domain":["x.com"],"ip":["10.0.0.0/8"],"port":"80"}' });
    const shape = intentToRule(before, () => 'wg-home');
    const after = expectResolved(ruleToIntent(shape, () => 2));

    expect(JSON.parse(after.criteria)).toEqual({
      domain: ['x.com'], ip: ['10.0.0.0/8'], port: '80',
    });
  });

  it('keeps a balancer destination a balancer', () => {
    const shape = intentToRule(record({ destKind: 'balancer', destTag: 'lb' }), () => 'wg-home');
    expect(shape.balancerTag).toBe('lb');
    expect(expectResolved(ruleToIntent(shape, () => 2)).destKind).toBe('balancer');
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

/*
An attrs criterion must survive the round-trip the editor puts it through.

`attrs` is the one criterion whose xray shape is an OBJECT. The bridge between a
stored intent row and the rule shape the editor edits flattened every criterion
with String(value), so an attrs map became the literal "[object Object]" on the
way out — and the editor then split that string into one row per character. On
the way back, criteriaFromForm ran the same flatten and stored the mangled
string. Whatever the operator had configured was gone, replaced by junk.

Both legs are pinned here because fixing one alone leaves the value mangled in
the other direction. F188.
*/
describe('an attrs criterion survives the editor round-trip', () => {
  const attrs = { ':method': 'GET', ':path': '^/api/' };

  it('reaches the rule shape as an object, not a stringified one', () => {
    const rule = intentToRule(
      {
        id: 1, enable: true, remark: 'r', ingressScope: 'all', ingressIds: [],
        destKind: 'outbound', destTag: 'direct',
        criteria: JSON.stringify({ attrs }),
      } as never,
      () => 'in-1',
    );
    expect(rule.attrs, 'the attrs object was dropped or stringified').toEqual(attrs);
    expect(JSON.stringify(rule)).not.toContain('[object Object]');
  });

  it('comes back from the rule shape unchanged', () => {
    const back = ruleToIntent({ type: 'field', attrs, outboundTag: 'direct', inboundTag: [] }, () => 1);
    expect(back.ok, 'ruleToIntent refused a rule carrying attrs').toBe(true);
    if (!back.ok) return;
    const stored = JSON.parse(back.payload.criteria as string) as Record<string, unknown>;
    expect(stored.attrs, 'attrs did not survive the write leg').toEqual(attrs);
  });

  it('ignores a stored value that is not an object', () => {
    const rule = intentToRule(
      {
        id: 1, enable: true, remark: 'r', ingressScope: 'all', ingressIds: [],
        destKind: 'outbound', destTag: 'direct',
        criteria: JSON.stringify({ attrs: 'not-an-object' }),
      } as never,
      () => 'in-1',
    );
    expect(rule.attrs, 'a malformed stored value became a rule the editor expands per character').toBeUndefined();
  });
});
