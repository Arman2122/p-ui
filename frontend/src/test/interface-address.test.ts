import { describe, it, expect } from 'vitest';

import { normalizeInterfaceAddress, normalizeInterfaceAddresses } from '@/lib/xray/interface-address';

/*
The server drops an unparseable interface address in silence, so the device ends
up answering on nothing and no screen says why. Every rejection here is one the
operator gets told about instead.
*/
describe('normalizeInterfaceAddress', () => {
  it('keeps a well-formed prefix as typed', () => {
    for (const value of ['10.0.0.1/24', '10.90.4.1/22', 'fd00::1/64', '192.168.1.1/16']) {
      expect(normalizeInterfaceAddress(value), value).toEqual({ ok: true, value });
    }
  });

  /* A bare address on this field can only mean "with an ordinary subnet", and
     /24 is the panel's own default base. */
  it('completes a bare address rather than refusing it', () => {
    expect(normalizeInterfaceAddress('10.0.0.1')).toEqual({ ok: true, value: '10.0.0.1/24' });
    expect(normalizeInterfaceAddress('fd00::1')).toEqual({ ok: true, value: 'fd00::1/64' });
  });

  it('trims what a paste leaves behind', () => {
    expect(normalizeInterfaceAddress('  10.0.0.1/24 ')).toEqual({ ok: true, value: '10.0.0.1/24' });
  });

  it('refuses what is not an address at all', () => {
    for (const value of ['', '   ', 'abc', '10.0.0', '10.0.0.256/24', 'not/24', '10.0.0.1/x']) {
      expect(normalizeInterfaceAddress(value).ok, value).toBe(false);
    }
  });

  it('refuses a prefix length outside the family', () => {
    expect(normalizeInterfaceAddress('10.0.0.1/33')).toMatchObject({
      ok: false, reason: 'pages.inbounds.form.wgkernelAddressPrefixRange',
    });
    expect(normalizeInterfaceAddress('fd00::1/129')).toMatchObject({
      ok: false, reason: 'pages.inbounds.form.wgkernelAddressPrefixRange',
    });
  });

  /* A /32 device address routes nobody: the pool it is meant to cover has no
     room in it, so every client would end up on a per-peer route. */
  it('refuses a device address with no room for a client', () => {
    expect(normalizeInterfaceAddress('10.0.0.1/32')).toMatchObject({
      ok: false, reason: 'pages.inbounds.form.wgkernelAddressNoRoom',
    });
  });

  it('accepts a v6 prefix at its own maximum', () => {
    expect(normalizeInterfaceAddress('fd00::1/128')).toEqual({ ok: true, value: 'fd00::1/128' });
  });
});

describe('normalizeInterfaceAddresses', () => {
  it('keeps the good entries in order and names what it dropped', () => {
    const got = normalizeInterfaceAddresses(['10.0.0.1/24', 'nonsense', 'fd00::1']);
    expect(got.values).toEqual(['10.0.0.1/24', 'fd00::1/64']);
    expect(got.rejected).toEqual(['nonsense']);
    expect(got.reason).toBe('pages.inbounds.form.wgkernelAddressInvalid');
  });

  it('collapses a duplicate rather than letting the device carry it twice', () => {
    const got = normalizeInterfaceAddresses(['10.0.0.1/24', '10.0.0.1']);
    expect(got.values).toEqual(['10.0.0.1/24']);
  });

  it('says nothing when everything is good', () => {
    expect(normalizeInterfaceAddresses(['10.0.0.1/24']).reason).toBeNull();
  });
});
