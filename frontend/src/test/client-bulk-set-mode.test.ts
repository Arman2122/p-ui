import { describe, it, expect } from 'vitest';

import { ClientBulkAdjustFormSchema } from '@/schemas/client';

/*
The bulk dialog asks which question is being asked.

ADD answers "give everyone another 30 days"; SET answers "make it exactly 30
days from now". Reaching the second through the first needs the operator to know
every client's current value, so it was not reachable at all.

ADD stays the default and the first option, because it is what this dialog has
always done -- an operator reaching for it must not find SET under their muscle
memory. F-bulk / OPEN-DECISIONS §5.
*/
describe('bulk adjust mode', () => {
  it('defaults to add when the caller does not say', () => {
    const parsed = ClientBulkAdjustFormSchema.safeParse({ addDays: 30, addGB: 0, flow: '' });
    expect(parsed.success).toBe(true);
    if (parsed.success) expect(parsed.data.mode).toBe('add');
  });

  it('accepts set', () => {
    const parsed = ClientBulkAdjustFormSchema.safeParse({ mode: 'set', addDays: 0, addGB: 50, flow: '' });
    expect(parsed.success).toBe(true);
    if (parsed.success) expect(parsed.data.mode).toBe('set');
  });

  /* A typo must not silently become one of the two: on this dialog the two
     modes do opposite things to every selected client. */
  it('refuses a mode it does not know', () => {
    expect(ClientBulkAdjustFormSchema.safeParse({ mode: 'multiply', addDays: 1, addGB: 0, flow: '' }).success)
      .toBe(false);
  });

  /* Zero means "leave alone" in both modes, so a form with nothing filled in is
     still nothing to do -- in set mode too. */
  it('still refuses a submission that changes nothing', () => {
    expect(ClientBulkAdjustFormSchema.safeParse({ mode: 'set', addDays: 0, addGB: 0, flow: '' }).success)
      .toBe(false);
  });
});
