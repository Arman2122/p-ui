import { describe, expect, it } from 'vitest';

import { awgObfuscationChanged } from '@/lib/inbounds/awg-diff';

/*
Saving an AmneziaWG inbound with a changed obfuscation value cuts every
client off until it is re-issued, and the form said nothing. The form now
asks first, and this pins what counts as a change: any obfuscation value,
with absent, empty and false read as the same thing. Feature gap W6.
*/
describe('awgObfuscationChanged', () => {
  const before = { settings: { awg: { jc: 4, jmin: 40, jmax: 70, h1: '10-19', i1: '' } } };

  it('sees a changed junk count and a changed header range', () => {
    expect(awgObfuscationChanged(before, { settings: { awg: { ...before.settings.awg, jc: 5 } } })).toBe(true);
    expect(awgObfuscationChanged(before, { settings: { awg: { ...before.settings.awg, h1: '20-29' } } })).toBe(true);
  });

  it('ignores an untouched form and cosmetic differences', () => {
    expect(awgObfuscationChanged(before, before)).toBe(false);
    expect(awgObfuscationChanged(before, { settings: { awg: { ...before.settings.awg, i1: undefined, randomTrailers: false } } })).toBe(false);
    expect(awgObfuscationChanged({ settings: {} }, { settings: { awg: {} } })).toBe(false);
  });
});
