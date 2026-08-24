import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/*
Pins the call site, which a unit test on the helper cannot reach.

toggleRule lives inside a component with no test harness, so this reads the
source the way i18n-dead-keys.test.ts already does. What it guards is small and
easy to undo by accident: a toggle that goes back through intentToRule and
ruleToIntent recomputes scope, criteria and destination from a single flag
change, and that recomputation is what silently rewrote a scoped rule to every
inbound.
*/
describe('the routing rule toggle', () => {
  const source = readFileSync(
    resolve(__dirname, '../pages/xray/routing/RoutingTab.tsx'),
    'utf8',
  );

  const toggleBody = (() => {
    const start = source.indexOf('function toggleRule(');
    expect(start, 'toggleRule was renamed; this guard is now vacuous').toBeGreaterThan(-1);
    const end = source.indexOf('\n  function ', start + 1);
    return source.slice(start, end > -1 ? end : undefined);
  })();

  it('builds its payload from the stored row', () => {
    expect(toggleBody).toContain('recordToPayload(record)');
  });

  it('never rebuilds the rule through the editor shape', () => {
    expect(toggleBody).not.toContain('ruleToIntent');
    expect(toggleBody).not.toContain('intentToRule');
  });
});
