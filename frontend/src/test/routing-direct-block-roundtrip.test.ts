import { describe, expect, it } from 'vitest';

import { intentToRule, ruleToIntent, type RoutingRuleRecord } from '@/schemas/api/routing';
import { expectResolved } from '@/test/routing-intent-helpers';

/*
A direct or block rule stopped being one the moment it was opened.

intentToRule renders those two destinations as the fixed tags `direct` and
`blocked`; destinationOf, reading back, had no arm for either and mapped
anything that was not an exit or a balancer to destKind 'outbound' with the tag
as its value. So a stored direct rule opened in the editor and saved unchanged
came back as an outbound rule pointing at the literal string "direct".

On the default template that is invisible — config.json ships outbounds tagged
direct and blocked, so both readings resolve to the same thing. On a template
that does not, the rule now names an outbound nobody has, which is a rule that
routes nowhere and an editor that will not save it.

Both directions are asserted, because the whole defect is that they disagreed.
F201.
*/

const base: RoutingRuleRecord = {
  id: 1,
  sortIndex: 0,
  enable: true,
  remark: 'ads',
  ingressScope: 'all',
  ingressIds: '[]',
  destKind: 'block',
  destTag: '',
  destExitId: null,
  criteria: '{"domain":"geosite:category-ads"}',
  inspect: false,
};

describe('a direct or block destination', () => {
  it.each([
    ['block', 'blocked'],
    ['direct', 'direct'],
  ])('survives a round trip through the editor (%s)', (kind, tag) => {
    const record = { ...base, destKind: kind };

    const shape = intentToRule(record, () => undefined);
    expect(shape.outboundTag, 'the wire form is unchanged').toBe(tag);

    const back = expectResolved(ruleToIntent(shape, () => undefined));
    expect(
      back.destKind,
      `reading it back gave ${back.destKind}; on a template with no outbound named "${tag}" that rule points at nothing`,
    ).toBe(kind);
    expect(back.destTag).toBe('');
  });

  it('still reads an ordinary outbound as an outbound', () => {
    const shape = intentToRule({ ...base, destKind: 'outbound', destTag: 'proxy-de' }, () => undefined);
    const back = expectResolved(ruleToIntent(shape, () => undefined));
    expect(back.destKind).toBe('outbound');
    expect(back.destTag).toBe('proxy-de');
  });
});
