import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { headerMapNamesHost, kcpSendingWindowFits } from '@/lib/xray/transport-limits';

/*
Two transport values the core refuses outright, and the forms offered both.

Neither is a field-level annoyance: xray aborts the WHOLE config on either, so
every inbound of every protocol stops answering at the next restart, and the
operator's only clue was a raw error bounced back from the save. F324 / F325.
*/

describe('mKCP send window against the MTU', () => {
  it('refuses a window below the MTU', () => {
    // xray computes the buffer as maxSendingWindow / mtu; below the MTU that is
    // zero buffers and the config is refused.
    expect(kcpSendingWindowFits(575, 1350)).toBe(false);
    expect(kcpSendingWindowFits(0, 576)).toBe(false);
    expect(kcpSendingWindowFits(1349, 1350)).toBe(false);
  });

  it('accepts a window the MTU divides into at least once', () => {
    expect(kcpSendingWindowFits(1350, 1350)).toBe(true);
    expect(kcpSendingWindowFits(2097152, 1350)).toBe(true);
    expect(kcpSendingWindowFits(576, 576)).toBe(true);
  });

  it('says nothing when it cannot tell', () => {
    // A half-typed form must not light up red on a field the operator has not
    // reached yet.
    expect(kcpSendingWindowFits(undefined, 1350)).toBe(true);
    expect(kcpSendingWindowFits(1000, undefined)).toBe(true);
    expect(kcpSendingWindowFits(1000, 0)).toBe(true);
  });
});

describe('a host header on httpupgrade or xhttp', () => {
  it('is caught whatever its case or spacing', () => {
    expect(headerMapNamesHost({ Host: 'example.com' })).toBe(true);
    expect(headerMapNamesHost({ host: 'example.com' })).toBe(true);
    expect(headerMapNamesHost({ HOST: 'example.com' })).toBe(true);
    expect(headerMapNamesHost({ ' Host ': 'example.com' })).toBe(true);
  });

  it('leaves every other header alone', () => {
    expect(headerMapNamesHost({ 'X-Forwarded-Host': 'example.com' })).toBe(false);
    expect(headerMapNamesHost({ 'User-Agent': 'curl' })).toBe(false);
    expect(headerMapNamesHost({})).toBe(false);
    expect(headerMapNamesHost(undefined)).toBe(false);
  });
});

// And every form that can produce either value runs the check — a predicate
// nothing calls fixes nothing, and there are four header sites, not one.
describe('the forms that can produce them', () => {
  const read = (p: string) => readFileSync(resolve(__dirname, '..', p), 'utf8');

  it.each([
    'pages/inbounds/form/transport/kcp.tsx',
    'pages/xray/outbounds/transport/kcp.tsx',
  ])('%s checks the send window against the MTU', (file) => {
    expect(read(file)).toContain('kcpSendingWindowFits(value, mtu)');
  });

  it.each([
    'pages/inbounds/form/transport/httpupgrade.tsx',
    'pages/xray/outbounds/transport/httpupgrade.tsx',
    'pages/inbounds/form/transport/xhttp.tsx',
    'pages/xray/outbounds/transport/xhttp.tsx',
  ])('%s refuses a host header', (file) => {
    expect(read(file)).toContain('headerMapNamesHost(value)');
  });
});
