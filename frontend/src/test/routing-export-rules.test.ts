import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { exportableRuleList } from '@/pages/xray/routing/helpers';
import type { RoutingRuleRecord, XrayRuleShape } from '@/schemas/api/routing';

/*
Export used to serialise the template's routing.rules and nothing else.

The table shows two stores merged: the rules an operator writes in the dialog go
to the panel's own store, and only what came in with a template lives in
routing.rules. So the export was missing every rule the operator had created --
on a panel where all the rules were made here, that is all of them.

It failed silently, which is the part that matters: the file it produced was
valid JSON in the right shape, and the operator only found out when they
restored from it. The menu item was disabled on the same wrong count, so a panel
whose rules were all its own could not export at all. F192.
*/

const OPERATOR_RULE: RoutingRuleRecord = {
  id: 3,
  sortIndex: 0,
  enable: true,
  remark: 'ads to nowhere',
  ingressScope: 'all',
  ingressIds: '[]',
  destKind: 'block',
  destTag: '',
  destExitId: null,
  criteria: '{"domain":"geosite:category-ads"}',
  inspect: false,
};

const TEMPLATE_RULE: XrayRuleShape = {
  type: 'field',
  outboundTag: 'direct',
  domain: ['geosite:private'],
};

describe('exporting the routing rules', () => {
  it('carries the operator’s own rules, ahead of the template’s', () => {
    const out = exportableRuleList([OPERATOR_RULE], [TEMPLATE_RULE], () => undefined);

    expect(out, 'the export dropped a rule the table shows').toHaveLength(2);
    expect(out[0].remark).toBe('ads to nowhere');
    expect(out[0].outboundTag).toBe('blocked');
    expect(out[0].domain).toEqual(['geosite:category-ads']);
    /* Order is not cosmetic: routing is first-match, so a re-import that put
       the template's rules first would decide differently. */
    expect(out[1].outboundTag).toBe('direct');
  });

  it('is empty only when there is genuinely nothing to export', () => {
    expect(exportableRuleList([], [], () => undefined)).toHaveLength(0);
    expect(exportableRuleList([OPERATOR_RULE], [], () => undefined)).toHaveLength(1);
  });
});

// And the page really exports that list — the property a unit test on the
// helper cannot see, and the one that was wrong for the whole of this feature.
describe('the export menu item', () => {
  const source = readFileSync(
    resolve(__dirname, '../pages/xray/routing/RoutingTab.tsx'),
    'utf8',
  );

  it('serialises the merged list, and greys out on its count', () => {
    expect(source).toContain('JSON.stringify(exportableRules');
    expect(
      source.includes("disabled: rules.length === 0, onClick: exportRules"),
      'the item greys out on the template count, so a panel whose rules are all its own cannot export',
    ).toBe(false);
  });
});
