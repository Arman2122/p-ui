import { describe, expect, it } from 'vitest';

import { genWireguardConfigs, genWireguardLinks, wireguardConfigFromLink } from '@/lib/xray/inbound-link';
import { awgInterfaceLines, linkCarriesObfuscation } from '@/lib/xray/awg-conf';

/*
The config the QR panel hands out must carry the obfuscation.

The panel builds a .conf in two places -- here for the QR and download panel,
and in Go for the subscription -- and only the Go one emitted these. A client
scanning the QR of an obfuscated AmneziaWG inbound got a config that completes
NO handshake: not slower, not degraded, silent on both sides with nothing to
explain it.
*/
const inbound = (awg?: Record<string, unknown>) => ({
  protocol: 'awgkernel',
  port: 2020,
  listen: '',
  settings: {
    secretKey: 'QFn9tYyPqSMYPO1jN0OFHqjJnJRvJhLj0kZ7Cw+CVWM=',
    dns: '9.9.9.9',
    mtu: 1420,
    clients: [{
      email: 'alice',
      privateKey: 'wDuF2kxpRZgkl4qkNhZ/KOGcN34UQjjyQ01NeHH86ls=',
      publicKey: 'fTSmEFT1+CxddOPozrFAXy/sz4Zj7prTJfPZpaU9pVk=',
      allowedIPs: ['10.0.0.2/32'],
      keepAlive: 25,
    }],
    ...(awg ? { awg } : {}),
  },
}) as never;

describe('the AmneziaWG client config the panel hands out', () => {
  it('carries the obfuscation inside [Interface]', () => {
    const conf = genWireguardConfigs({
      inbound: inbound({ jc: 4, jmin: 40, jmax: 70, s1: 20, s2: 30, randomTrailers: true }),
      remark: 'awg',
      fallbackHostname: 'vpn.example.com',
    });

    const iface = conf.slice(0, conf.indexOf('[Peer]'));
    for (const want of ['Jc = 4', 'Jmin = 40', 'Jmax = 70', 'S1 = 20', 'S2 = 30', 'RandomTrailers = on']) {
      expect(iface, `${want} missing from [Interface]:\n${conf}`).toContain(want);
    }
  });

  // An inbound with no obfuscation is plain WireGuard on the AmneziaWG module,
  // which is a legitimate thing to run and must not gain stray keys.
  it('writes nothing extra when there is no obfuscation', () => {
    const conf = genWireguardConfigs({
      inbound: inbound(),
      remark: 'plain',
      fallbackHostname: 'vpn.example.com',
    });
    for (const absent of ['Jc =', 'S1 =', 'RandomTrailers']) {
      expect(conf).not.toContain(absent);
    }
    expect(conf).toContain('[Interface]');
    expect(conf).toContain('[Peer]');
  });

  it('emits only the values that were set', () => {
    expect(awgInterfaceLines({ jc: 4 } as never)).toBe('Jc = 4\n');
    expect(awgInterfaceLines(undefined)).toBe('');
    expect(awgInterfaceLines({} as never)).toBe('');
  });
});

/*
The subscription page builds its downloadable config from the LINK, not from the
inbound. So a link that drops the obfuscation produces a config that cannot
connect to the server that issued it -- reported from the panel as "I downloaded
the config and it fails", with nothing on either side saying why.
*/
describe('the AmneziaWG share link', () => {
  it('carries the obfuscation, and the config built from it gets it back', () => {
    const link = genWireguardLinks({
      inbound: inbound({ jc: 4, jmin: 40, jmax: 70, s1: 20, s2: 30, i1: 'b0xdeadbeef' }),
      remark: 'awg',
      fallbackHostname: 'vpn.example.com',
    }).split('\r\n')[0];

    const params = new URL(link).searchParams;
    expect(params.get('jc'), `link dropped the obfuscation: ${link}`).toBe('4');
    expect(params.get('i1')).toBe('b0xdeadbeef');
    expect(linkCarriesObfuscation(params)).toBe(true);

    const conf = wireguardConfigFromLink(link, 'awg');
    const iface = conf.slice(0, conf.indexOf('[Peer]'));
    for (const want of ['Jc = 4', 'Jmin = 40', 'Jmax = 70', 'S1 = 20', 'S2 = 30', 'I1 = b0xdeadbeef']) {
      expect(iface, `${want} missing from the derived config:\n${conf}`).toContain(want);
    }
  });

  // A plain WireGuard link must stay plain, or every existing client's config
  // gains keys their app does not understand.
  it('leaves a plain WireGuard link alone', () => {
    const link = genWireguardLinks({
      inbound: inbound(),
      remark: 'plain',
      fallbackHostname: 'vpn.example.com',
    }).split('\r\n')[0];

    expect(linkCarriesObfuscation(new URL(link).searchParams)).toBe(false);
    expect(wireguardConfigFromLink(link, 'plain')).not.toContain('Jc =');
  });
});
