import { describe, expect, it } from 'vitest';

import {
  genWireguardConfig,
  genWireguardLink,
  wireguardConfigFromLink,
} from '@/lib/xray/inbound-link';
import { awgInterfaceLines } from '@/lib/xray/awg-conf';

/*
The WireGuard family is rendered four times — the frontend link, the frontend
.conf, the Go subscription link and the Go .conf — and they disagreed about
which fields exist.

These pin the two disagreements that were live in the browser. Both were
provable without leaving the frontend, which is what makes them worth a test
here rather than a cross-language corpus: each function contradicted a sibling
in its own file.
*/

const awgFull = {
  jc: 4,
  jmin: 10,
  jmax: 50,
  s1: 15,
  s2: 16,
  s3: 17,
  s4: 18,
  h1: '10-19',
  h2: '20-29',
  h3: '30-39',
  h4: '40-49',
  i1: '<b 0x01>',
  headerProtectionKey: 'a'.repeat(44),
  contentPaddingAddition: '5-9',
  rekeyAfterTime: '120',
  rekeyTimeout: '5',
  rejectAfterTime: '180',
  keepaliveTimeout: '25',
  maxHandshakeAttempts: '18',
  randomTrailers: true,
  disableCookies: true,
};

/*
awgInterfaceLines dropped H1-H4 and all six timers, while awgLinesFromLinkParams
— in the same file — has always listed every one of them. So the panel's own
downloadable .conf described a weaker device than the link beside it, and a
client importing it got WireGuard's default header values against a device
configured with custom ones. A mismatched pair does not degrade; it never
completes a handshake. F020 / F102 / F269.
*/
describe('the AmneziaWG .conf carries every parameter the link does', () => {
  const conf = awgInterfaceLines(awgFull);

  const required: [string, string][] = [
    ['H1', 'H1 = 10-19'],
    ['H2', 'H2 = 20-29'],
    ['H3', 'H3 = 30-39'],
    ['H4', 'H4 = 40-49'],
    ['ContentPaddingAddition', 'ContentPaddingAddition = 5-9'],
    ['RekeyAfterTime', 'RekeyAfterTime = 120'],
    ['RekeyTimeout', 'RekeyTimeout = 5'],
    ['RejectAfterTime', 'RejectAfterTime = 180'],
    ['KeepaliveTimeout', 'KeepaliveTimeout = 25'],
    ['MaxHandshakeAttempts', 'MaxHandshakeAttempts = 18'],
  ];
  required.forEach(([name, line]) => {
    it(`emits ${name}`, () => {
      expect(conf).toContain(line);
    });
  });

  // The fields that always worked must not have moved.
  it('still emits the junk and padding parameters', () => {
    expect(conf).toContain('Jc = 4');
    expect(conf).toContain('S1 = 15');
    expect(conf).toContain('HeaderProtectionKey = ' + 'a'.repeat(44));
  });

  // A cleared range is written by omission: the module supplies WireGuard's own
  // value, and an explicit zero would mean something different.
  it('omits a cleared range rather than writing a zero', () => {
    const cleared = awgInterfaceLines({ jc: 4, h1: 0, rekeyTimeout: '' });
    expect(cleared).toContain('Jc = 4');
    expect(cleared).not.toContain('H1');
    expect(cleared).not.toContain('RekeyTimeout');
  });

  it('is still empty for an inbound with no obfuscation', () => {
    expect(awgInterfaceLines({})).toBe('');
    expect(awgInterfaceLines(undefined)).toBe('');
  });
});

/*
genWireguardLink omitted dns, presharedkey and keepalive — the three keys
wireguardConfigFromLink reads back out of a link to rebuild a .conf. The
frontend disagreed with ITSELF: a config rebuilt from one of its own links fell
back to 1.1.1.1, no pre-shared key and no keepalive, and a peer whose server
requires a PSK cannot complete a handshake without it. F019.
*/
describe('a link carries what rebuilding a config from it needs', () => {
  const settings = {
    secretKey: 'OKBAiOJ5vBrDeuc9Wt3rXFm6BiHRlHXwMTVKpXQ0M0I=',
    dns: '9.9.9.9, 149.112.112.112',
    mtu: 1380,
    peers: [{
      privateKey: 'aFNCJKGb7fCcqA3+A4A6f3Xg0pCqL0hnVFsCXqJmU1o=',
      allowedIPs: ['10.0.0.2/32'],
      preSharedKey: 'PSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKPSKA=',
      keepAlive: 25,
    }],
  };
  const input = { settings, address: 'vpn.example.test', port: 51820, remark: 'peer-1', peerIndex: 0 };

  const link = genWireguardLink(input as Parameters<typeof genWireguardLink>[0]);
  const rebuilt = wireguardConfigFromLink(link);
  const direct = genWireguardConfig(input as Parameters<typeof genWireguardConfig>[0]);

  it('carries the DNS the inbound configured', () => {
    expect(link).toContain('dns=');
    expect(rebuilt).toContain('9.9.9.9');
  });

  it('carries the pre-shared key, without which the peer cannot handshake', () => {
    expect(link).toContain('presharedkey=');
    expect(rebuilt).toContain('PresharedKey');
  });

  it('carries the keepalive', () => {
    expect(link).toContain('keepalive=');
    expect(rebuilt).toContain('PersistentKeepalive = 25');
  });

  /*
    The whole point: a config rebuilt from the link and one built straight from
    the inbound must describe the same tunnel. They differed on exactly the
    three fields above, and nothing compared them.
  */
  it('rebuilds the same DNS, PSK and keepalive the direct config has', () => {
    for (const marker of ['9.9.9.9', 'PresharedKey', 'PersistentKeepalive']) {
      expect(direct, `direct config lost ${marker}`).toContain(marker);
      expect(rebuilt, `rebuilt config lost ${marker}`).toContain(marker);
    }
  });
});
