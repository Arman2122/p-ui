import { describe, it, expect } from 'vitest';

import { intentToRule, ruleToIntent } from '@/schemas/api/routing';
import type { RoutingRuleRecord } from '@/schemas/api/routing';

const RECORD: RoutingRuleRecord = {
  id: 7,
  sortIndex: 0,
  enable: true,
  remark: 'ads to the blackhole',
  ingressScope: 'all',
  ingressIds: '[]',
  destKind: 'block',
  destTag: '',
  destExitId: null,
  criteria: JSON.stringify({ domain: 'ads.example' }),
  inspect: false,
};

const tagOf = () => undefined;
const idOf = () => undefined;

/* Every edit and every enable-toggle sends the rule through intentToRule and
   back through ruleToIntent. A key the first drops is a key the POST blanks. */
describe('a rule survives the editor round trip', () => {
  it('keeps the remark, which the panel also writes its own disable reason into', () => {
    const shape = intentToRule(RECORD, tagOf);
    expect(shape.remark).toBe('ads to the blackhole');
    expect(ruleToIntent(shape, idOf).remark).toBe('ads to the blackhole');
  });

  it('keeps it across a toggle, which never opens the form at all', () => {
    const shape = intentToRule(RECORD, tagOf);
    const payload = { ...ruleToIntent(shape, idOf), enable: false };
    expect(payload.remark).toBe('ads to the blackhole');
    expect(payload.enable).toBe(false);
  });

  /* The modal rebuilds the fields it manages and copies the rest verbatim, so
     the remark only survives the edit path if intentToRule emitted it. */
  it('keeps it through the modal, which carries only keys it does not manage', () => {
    const shape = intentToRule(RECORD, tagOf);
    const managed = new Set(['type', 'enabled', 'domain', 'ip', 'port', 'sourcePort',
      'vlessRoute', 'network', 'sourceIP', 'user', 'inboundTag', 'protocol', 'attrs',
      'outboundTag', 'balancerTag']);
    const carried: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(shape)) {
      if (!managed.has(key) && value !== undefined) carried[key] = value;
    }
    expect(carried.remark).toBe('ads to the blackhole');
  });

  it('leaves an empty remark empty rather than inventing one', () => {
    const shape = intentToRule({ ...RECORD, remark: '' }, tagOf);
    expect(ruleToIntent(shape, idOf).remark).toBe('');
  });
});
