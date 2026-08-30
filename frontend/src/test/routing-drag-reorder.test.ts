import { describe, it, expect } from 'vitest';

import { canSwapRows, originalRuleIndex } from '@/pages/xray/routing/helpers';
import type { RuleRow } from '@/pages/xray/routing/types';

/*
The table shows one merged list — operator rules first, then the template's —
but the two are stored separately. Resolving a MERGED index against the
template-only rows is what silently reordered a template rule when the operator
dragged one of their own.
*/
const INTENT: RuleRow[] = [
  { key: 10000, source: 'intent', storeIndex: 0 },
  { key: 10001, source: 'intent', storeIndex: 1 },
];
/* key is the index in the UNFILTERED template array: a hidden balancer-loopback
   rule sits at 1, so the second visible template row stores at 2. */
const TEMPLATE: RuleRow[] = [
  { key: 0, source: 'template', storeIndex: 0 },
  { key: 2, source: 'template', storeIndex: 2 },
];
const MERGED: RuleRow[] = [...INTENT, ...TEMPLATE];

/* The source-aware resolution the drag handler performs. It calls the REAL
   predicate rather than a copy: a local re-implementation stays green while
   the production guard is removed, which is what let the button path ship
   without it. */
function resolveDrag(from: number, to: number) {
  const fromRow = MERGED[from];
  const toRow = MERGED[to];
  if (!canSwapRows(fromRow, toRow)) return null;
  return { list: fromRow.source, from: fromRow.storeIndex, to: toRow.storeIndex };
}

describe('dragging a rule moves that rule', () => {
  it('resolves an operator drag against the operator list', () => {
    expect(resolveDrag(0, 1)).toEqual({ list: 'intent', from: 0, to: 1 });
  });

  it('resolves a template drag against the template list, past hidden rows', () => {
    expect(resolveDrag(2, 3)).toEqual({ list: 'template', from: 0, to: 2 });
  });

  it('refuses a drag across the boundary, which names no position in either list', () => {
    expect(resolveDrag(1, 2)).toBeNull();
    expect(resolveDrag(3, 0)).toBeNull();
  });

  /* The regression itself: merged index 2 is the FIRST template row, but read
     against the template-only rows it lands on the second one. */
  it('is not what reading a merged index against the template rows would give', () => {
    const viaTemplateRows = originalRuleIndex(TEMPLATE, 2);
    const correct = resolveDrag(2, 3)!.from;
    expect(correct).toBe(0);
    expect(viaTemplateRows).not.toBe(correct);
  });
});

/*
The Move up / Move down buttons must refuse the same crossing the drag does.

A row's storeIndex is a position in ITS OWN store, and the table merges two
independently-ordered lists into one evaluation order. moveUp took the
neighbour's storeIndex as a bare number and indexed the TEMPLATE array with it,
guarded only by an existence check.

On the first template row the neighbour is the last operator rule, so that
number belongs to a different list. The swap went ahead and reordered two
template rules the operator never touched. Routing is first-match, so it changes
which rule decides the traffic, and nothing on screen says it happened.

The drag path had declared this guard since it was written. Both paths now call
the same predicate, and the source check below is what stops one of them
drifting away from it again. F189.
*/
describe('canSwapRows', () => {
  it('refuses a swap across the operator/template boundary', () => {
    expect(canSwapRows(INTENT[1], TEMPLATE[0])).toBe(false);
    expect(canSwapRows(TEMPLATE[0], INTENT[1])).toBe(false);
  });

  it('allows a swap inside one store', () => {
    expect(canSwapRows(INTENT[0], INTENT[1])).toBe(true);
    expect(canSwapRows(TEMPLATE[0], TEMPLATE[1])).toBe(true);
  });

  it('refuses a missing row', () => {
    expect(canSwapRows(undefined, TEMPLATE[0])).toBe(false);
    expect(canSwapRows(TEMPLATE[0], undefined)).toBe(false);
  });
});

/*
And every reorder path must go through it.

The predicate above is only worth anything if all three callers use it. This is
a source check because the alternative -- rendering RoutingTab and driving its
buttons -- tests the table library, and because the defect was precisely that
one path re-implemented the rule and one forgot it entirely.
*/
describe('every reorder path uses the shared predicate', () => {
  it('moveUp, moveDown and the drag handler all call canSwapRows', async () => {
    const src = await import('@/pages/xray/routing/RoutingTab.tsx?raw').then((m) => m.default as string);

    for (const fn of ['function moveUp(', 'function moveDown(']) {
      const start = src.indexOf(fn);
      expect(start, `${fn} was renamed; this guard is now vacuous`).toBeGreaterThan(-1);
      const body = src.slice(start, src.indexOf('\n  }', start));
      expect(
        body.includes('canSwapRows('),
        `${fn} reorders without the cross-store check, so moving the first template row swaps two unrelated template rules`,
      ).toBe(true);
    }

    /* moveUp, moveDown and the drag handler; the import carries no paren. */
    expect(src.split('canSwapRows(').length - 1).toBe(3);
  });
});

/*
And the api rule cannot be moved at all.

Its drag handle was rendered like every other row's, and moveUp/moveDown were
gated on index alone -- only the enable Switch knew about it. So a template rule
could be dragged above it, the table redrew in the new order, and the save then
put the api rule back at the top (EnsureStatsRouting, xray_setting.go), with no
response field and no toast saying so. The order on screen was not the order
stored, and routing is first-match.

The stats API rule really does have to lead: it is what the traffic counters
read. So the honest fix is to stop offering the move, not to accept it. F200.
*/
const API_RULE: RuleRow = {
  key: 0, source: 'template', storeIndex: 0, inboundTag: 'api', outboundTag: 'api',
};
const ORDINARY: RuleRow = { key: 1, source: 'template', storeIndex: 1 };

describe('the stats api rule', () => {
  it('cannot be swapped in either direction', () => {
    expect(
      canSwapRows(ORDINARY, API_RULE),
      'a rule was allowed above the api rule; the save silently puts it back and the table then lies about the order',
    ).toBe(false);
    expect(canSwapRows(API_RULE, ORDINARY)).toBe(false);
  });

  it('still lets two ordinary template rules swap', () => {
    expect(canSwapRows(ORDINARY, { ...ORDINARY, key: 2, storeIndex: 2 })).toBe(true);
  });

  it('is rendered without a drag handle', async () => {
    const src = await import('@/pages/xray/routing/useRoutingColumns.tsx?raw').then((m) => m.default as string);
    const at = src.indexOf('<HolderOutlined');
    expect(at, 'the drag handle was renamed; this guard is now vacuous').toBeGreaterThan(-1);
    expect(
      src.slice(Math.max(0, at - 200), at),
      'the api rule is handed a drag handle for a move the panel undoes on save',
    ).toContain('isApiRule(');
  });
});
