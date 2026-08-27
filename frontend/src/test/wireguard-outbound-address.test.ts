import { describe, expect, it } from 'vitest';

import { parseOutboundLink } from '@/lib/xray/outbound-link-parser';
import { formValuesToWirePayload } from '@/lib/xray/outbound-form-adapter';

/*
An imported WireGuard outbound must reach the payload with no address key at all.

`address` is the tunnel interface's OWN local address. Xray picks a bogon when
the key is ABSENT (infra/conf/wireguard.go:80) — but a JSON `[]` is a non-nil
empty slice, so it takes the else branch and leaves the outbound with no
endpoint whatsoever.

Fixing only the parser was not enough, and an audit of that fix caught it: the
parser omitted the key, then `wireguardToWire` put `address: []` straight back
on submit. The saved outbound was byte-identical to before. This test drives the
whole browser-side chain — parse, then the value that is actually sent — because
that is where the first attempt silently failed. F080.
*/

const LINK_WITHOUT_ADDRESS =
  'wireguard://cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA@vpn.example:51820'
  + '?publickey=cHVia2V5&allowedips=0.0.0.0%2F0#peer';

const LINK_WITH_ADDRESS =
  'wireguard://cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA@vpn.example:51820'
  + '?publickey=cHVia2V5&address=10.7.0.2%2F32,fd00::2%2F128#peer';

function wireSettings(link: string): Record<string, unknown> {
  const parsed = parseOutboundLink(link);
  expect(parsed, `the parser rejected ${link}`).not.toBeNull();
  const settings = (parsed as Record<string, unknown>).settings;
  return settings as Record<string, unknown>;
}

describe('a wireguard outbound imported from a link', () => {
  it('omits the address key when the link carries none', () => {
    const settings = wireSettings(LINK_WITHOUT_ADDRESS);
    expect(
      Object.prototype.hasOwnProperty.call(settings, 'address'),
      'the key must be absent so xray supplies its own bogon; [] defeats that and 0.0.0.0/0 is an allowedIPs value',
    ).toBe(false);
  });

  it('keeps an address the link does carry', () => {
    const settings = wireSettings(LINK_WITH_ADDRESS);
    expect(settings.address).toEqual(['10.7.0.2/32', 'fd00::2/128']);
  });

  /*
    The half the first attempt missed: the form round-trip. A blank address field
    must produce a payload with no address key, not an empty array.
  */
  it('does not put an empty address back on the way out', () => {
    const values = {
      protocol: 'wireguard',
      tag: 'wg-out',
      settings: {
        secretKey: 'cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA',
        address: '',
        mtu: 1420,
        domainStrategy: '',
        reserved: '',
        peers: [{ publicKey: 'cHVia2V5', psk: '', allowedIPs: '0.0.0.0/0', endpoint: 'vpn.example:51820', keepAlive: 0 }],
      },
    };
    const payload = formValuesToWirePayload(values as Parameters<typeof formValuesToWirePayload>[0]);
    /* Asserted on the SERIALISED payload, because that is what reaches the
       panel. A key whose value is undefined survives hasOwnProperty and is
       dropped by JSON.stringify, so only this says what the server receives. */
    const settings = JSON.parse(JSON.stringify(payload)).settings as Record<string, unknown>;
    expect(
      Object.prototype.hasOwnProperty.call(settings, 'address'),
      'the form put address: [] back, which is the shape that leaves the outbound with no endpoint',
    ).toBe(false);
  });

  it('still sends an address the operator typed', () => {
    const values = {
      protocol: 'wireguard',
      tag: 'wg-out',
      settings: {
        secretKey: 'cHJpdmF0ZWtleWJhc2U2NGVuY29kZWQxMjM0NTY3OA',
        address: '10.7.0.2/32, fd00::2/128',
        mtu: 1420,
        domainStrategy: '',
        reserved: '',
        peers: [{ publicKey: 'cHVia2V5', psk: '', allowedIPs: '0.0.0.0/0', endpoint: 'vpn.example:51820', keepAlive: 0 }],
      },
    };
    const payload = formValuesToWirePayload(values as Parameters<typeof formValuesToWirePayload>[0]);
    const settings = JSON.parse(JSON.stringify(payload)).settings as Record<string, unknown>;
    expect(settings.address).toEqual(['10.7.0.2/32', 'fd00::2/128']);
  });
});
