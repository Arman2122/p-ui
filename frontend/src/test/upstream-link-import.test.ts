import { describe, expect, it } from 'vitest';

import { parseHysteria2Link, parseShadowsocksLink, parseVlessLink, parseVmessLink } from '@/lib/xray/outbound-link-parser';
import { Base64 } from '@/utils';
import { genHysteriaLink } from '@/lib/xray/inbound-link';
import { InboundSchema } from '@/schemas/api/inbound';

describe('share-link import compatibility', () => {
  it('exports standard Gecko and migrated UDP hopping parameters', () => {
    const inbound = InboundSchema.parse({
      port: 443, protocol: 'hysteria', settings: { clients: [] },
      streamSettings: { network: 'hysteria', security: 'tls',
        hysteriaSettings: {},
        tlsSettings: { settings: { fingerprint: '' } },
        finalmask: { udp: [
          { type: 'salamander', settings: { password: 'pw', packetSize: '512-1200' } },
          { type: 'udphop', settings: { mode: 'intervalLocal,intervalRemote', remotePorts: '2000-3000' } },
        ] },
      },
    });
    const params = new URL(genHysteriaLink({ inbound, address: 'example.com', clientAuth: 'secret' })).searchParams;
    expect(Object.fromEntries(params)).toMatchObject({ obfs: 'gecko', 'obfs-password': 'pw', minPacketSize: '512', maxPacketSize: '1200', mport: '2000-3000' });
    expect(params.has('fm')).toBe(false);
    expect(params.has('fp')).toBe(false);
  });

  it('preserves VMess certificate verification, ECH, XHTTP mode and masks', () => {
    const link = `vmess://${Base64.encode(JSON.stringify({
      add: 'example.com', port: 443, id: 'uuid', net: 'xhttp', type: 'stream-up', tls: 'tls',
      ech: 'ech-config', vcn: 'verify.example', pcs: 'pin-one,pin-two',
      fm: JSON.stringify({ tcp: [{ type: 'fragment', settings: { packets: 'tlshello' } }] }),
    }))}`;
    expect(parseVmessLink(link)?.streamSettings).toMatchObject({
      xhttpSettings: { mode: 'stream-up' },
      tlsSettings: { echConfigList: 'ech-config', verifyPeerCertByName: 'verify.example', pinnedPeerCertSha256: 'pin-one,pin-two' },
      finalmask: { tcp: [{ type: 'fragment', settings: { packets: 'tlshello' } }] },
    });
  });

  it('preserves Shadowsocks TLS and transport on a SIP002 URL with a trailing slash', () => {
    const link = `ss://${Base64.encode('aes-128-gcm:password')}@example.com:8443/?type=ws&security=tls&path=%2Fsocket&host=cdn.example&sni=tls.example&fp=firefox&alpn=h2&pcs=pin`;
    expect(parseShadowsocksLink(link)).toMatchObject({
      settings: { servers: [{ address: 'example.com', port: 8443, password: 'password', method: 'aes-128-gcm' }] },
      streamSettings: { network: 'ws', security: 'tls', wsSettings: { path: '/socket', host: 'cdn.example' }, tlsSettings: { serverName: 'tls.example', fingerprint: 'firefox', alpn: ['h2'], pinnedPeerCertSha256: 'pin' } },
    });
  });

  it.each([encodeURIComponent('obfs-local;obfs=http;obfs-host=cdn.example'), 'obfs-local;obfs=http;obfs-host=cdn.example'])('restores SIP002 HTTP obfuscation: %s', (plugin) => {
    const link = `ss://${Base64.encode('aes-128-gcm:password')}@example.com:443?plugin=${plugin}`;
    expect(parseShadowsocksLink(link)?.streamSettings).toMatchObject({
      network: 'tcp', tcpSettings: { header: { type: 'http', request: { headers: { Host: ['cdn.example'] } } } },
    });
  });

  it.each(['ws', 'grpc', 'kcp'])('keeps an explicit %s transport when a SIP002 plugin is also present', (network) => {
    const link = `ss://${Base64.encode('aes-128-gcm:password')}@example.com:443?type=${network}&path=%2Fproxy&host=transport.example&plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dplugin.example`;
    const stream = parseShadowsocksLink(link)?.streamSettings;
    expect(stream).toMatchObject({ network, [`${network}Settings`]: expect.any(Object) });
    expect(stream).not.toHaveProperty('tcpSettings');
    if (network === 'ws') expect(stream).toMatchObject({ wsSettings: { host: 'transport.example', path: '/proxy' } });
  });

  it.each(['obfs-password', 'obfs_password', 'obfsPassword'])('imports Hysteria2 salamander and port hopping using %s', (key) => {
    expect(parseHysteria2Link(`hy2://secret@example.com:443?obfs=Salamander&${key}=obfs-secret&mport=2000-3000`)?.streamSettings).toMatchObject({
      finalmask: { udp: [
        { type: 'salamander', settings: { password: 'obfs-secret' } },
        { type: 'udphop', settings: { remotePorts: '2000-3000', interval: '5-10' } },
      ] },
    });
  });

  it('keeps explicit masks authoritative and completes a passwordless salamander mask', () => {
    const masks = { udp: [{ type: 'salamander', settings: { packetSize: '80-100' } }, { type: 'udphop', settings: { remotePorts: '8000-9000', mode: 'intervalRemote' } }] };
    const out = parseHysteria2Link(`hy2://secret@example.com:443?obfs=salamander&obfs-password=pw&mport=2000-3000&fm=${encodeURIComponent(JSON.stringify(masks))}`);
    expect(out?.streamSettings).toMatchObject({ finalmask: { udp: [
      { type: 'salamander', settings: { packetSize: '80-100', password: 'pw' } },
      { type: 'udphop', settings: { remotePorts: '8000-9000', mode: 'intervalRemote' } },
    ] } });
  });

  it.each([['64', '128', '64-128'], [' 64 ', ' 128 ', '64-128'], ['0', '128', undefined], ['128', '64', undefined], ['64', '2049', undefined]])('bounds Gecko packet sizes %s-%s', (min, max, packetSize) => {
    const out = parseHysteria2Link(`hy2://secret@example.com:443?obfs=gecko&obfs-password=pw&minPacketSize=${encodeURIComponent(min ?? '')}&maxPacketSize=${encodeURIComponent(max ?? '')}`);
    expect(out?.streamSettings).toMatchObject({ finalmask: { udp: [{ type: 'salamander', settings: { password: 'pw', ...(packetSize ? { packetSize } : {}) } }] } });
    const stream = out?.streamSettings as Record<string, unknown>;
    const mask = (stream.finalmask as { udp: Array<{ settings: Record<string, unknown> }> }).udp[0];
    expect(mask.settings.packetSize).toBe(packetSize);
  });

  it.each([
    [{ password: 'from-fm', packetSize: '80-100' }, { password: 'from-fm', packetSize: '80-100' }],
    [{ password: 'from-fm' }, { password: 'from-fm', packetSize: '64-128' }],
    [{ packetSize: '80-100' }, { password: 'from-query', packetSize: '80-100' }],
  ])('fills only missing Gecko mask fields from standard parameters: %j', (settings, expected) => {
    const finalmask = { udp: [{ type: 'salamander', settings }] };
    const out = parseHysteria2Link(`hy2://secret@example.com:443?obfs=gecko&obfs-password=from-query&minPacketSize=64&maxPacketSize=128&fm=${encodeURIComponent(JSON.stringify(finalmask))}`);
    expect((out?.streamSettings as Record<string, unknown>).finalmask).toEqual({ udp: [{ type: 'salamander', settings: expected }] });
  });

  it('restores mKCP encryption before its header mask and preserves supported timing', () => {
    expect(parseVlessLink('vless://uuid@example.com:443?type=kcp&headerType=wechat-video&seed=secret&mtu=1400&tti=50')?.streamSettings).toMatchObject({
      kcpSettings: { mtu: 1400, tti: 50 },
      finalmask: { udp: [
        { type: 'mkcp-legacy', settings: { header: '', value: 'secret' } },
        { type: 'mkcp-legacy', settings: { header: 'wechat', value: '' } },
      ] },
    });
  });

  it.each(['mtu=20&tti=9', 'mtu=2147483648&tti=1001', 'mtu=1e3&tti=0x20'])('keeps valid defaults for invalid mKCP values: %s', (query) => {
    expect(parseVlessLink(`vless://uuid@example.com:443?type=kcp&${query}`)?.streamSettings).toMatchObject({ kcpSettings: { mtu: 1350, tti: 20 } });
  });

  it('does not treat a prototype property as an mKCP header', () => {
    expect(parseVlessLink('vless://uuid@example.com:443?type=kcp&headerType=constructor')?.streamSettings).not.toHaveProperty('finalmask');
  });
});
