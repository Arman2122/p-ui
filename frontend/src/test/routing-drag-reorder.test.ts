import { describe, it, expect } from 'vitest';

import { originalRuleIndex } from '@/pages/xray/routing/helpers';
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

/* The source-aware resolution the drag handler performs. */
function resolveDrag(from: number, to: number) {
  const fromRow = MERGED[from];
  const toRow = MERGED[to];
  if (!fromRow || !toRow || fromRow.source !== toRow.source) return null;
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
