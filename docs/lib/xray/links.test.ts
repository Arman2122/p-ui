import { describe, it, expect } from 'vitest';
import {
  parseLink,
  detectProtocol,
  buildVless,
  buildVmess,
  buildTrojan,
  buildShadowsocks,
} from './links';

describe('detectProtocol', () => {
  it('recognises supported schemes', () => {
    expect(detectProtocol('vless://x')).toBe('vless');
    expect(detectProtocol('vmess://x')).toBe('vmess');
    expect(detectProtocol('trojan://x')).toBe('trojan');
    expect(detectProtocol('ss://x')).toBe('ss');
    expect(detectProtocol('https://x')).toBeNull();
  });
});

describe('parseLink — vless', () => {
  const link =
    'vless://11111111-2222-3333-4444-555555555555@example.com:443?security=reality&pbk=abc&sid=ff&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision&type=tcp#My%20Node';

  it('extracts address, port, credential, name, and params', () => {
    const r = parseLink(link);
    expect(r.protocol).toBe('vless');
    expect(r.address).toBe('example.com');
    expect(r.port).toBe(443);
    expect(r.credential).toBe('11111111-2222-3333-4444-555555555555');
    expect(r.name).toBe('My Node');
    expect(r.params.security).toBe('reality');
    expect(r.params.pbk).toBe('abc');
    expect(r.params.flow).toBe('xtls-rprx-vision');
  });
});

describe('parseLink — trojan', () => {
  it('parses password and query params', () => {
    const r = parseLink('trojan://p%40ss@1.2.3.4:8443?sni=a.com&type=ws&path=%2Fws#t');
    expect(r.protocol).toBe('trojan');
    expect(r.credential).toBe('p@ss');
    expect(r.address).toBe('1.2.3.4');
    expect(r.port).toBe(8443);
    expect(r.params.path).toBe('/ws');
    expect(r.name).toBe('t');
  });
});

describe('parseLink — vmess', () => {
  it('decodes the base64 JSON payload', () => {
    const link = buildVmess({
      ps: 'tehran',
      add: 'host.example',
      port: 443,
      id: 'uuid-123',
      net: 'ws',
      tls: 'tls',
      host: 'cdn.example',
      path: '/v2',
    });
    const r = parseLink(link);
    expect(r.protocol).toBe('vmess');
    expect(r.name).toBe('tehran');
    expect(r.address).toBe('host.example');
    expect(r.port).toBe(443);
    expect(r.credential).toBe('uuid-123');
    expect(r.params.net).toBe('ws');
    expect(r.params.path).toBe('/v2');
  });
});

describe('parseLink — shadowsocks', () => {
  it('parses SIP002 with base64 userinfo', () => {
    const link = buildShadowsocks({
      method: 'aes-256-gcm',
      password: 's3cret',
      address: '192.0.2.1',
      port: 8388,
      name: 'ss-node',
    });
    const r = parseLink(link);
    expect(r.protocol).toBe('ss');
    expect(r.credential).toBe('aes-256-gcm:s3cret');
    expect(r.address).toBe('192.0.2.1');
    expect(r.port).toBe(8388);
    expect(r.name).toBe('ss-node');
  });

  it('parses the legacy fully-base64 form', () => {
    // base64("aes-128-gcm:pw@example.com:8388")
    const legacy =
      'ss://' + Buffer.from('aes-128-gcm:pw@example.com:8388').toString('base64') + '#legacy';
    const r = parseLink(legacy);
    expect(r.credential).toBe('aes-128-gcm:pw');
    expect(r.address).toBe('example.com');
    expect(r.port).toBe(8388);
    expect(r.name).toBe('legacy');
  });
});

describe('build → parse round-trips', () => {
  it('vless round-trips', () => {
    const link = buildVless({
      credential: 'uuid-abc',
      address: 'example.com',
      port: 443,
      params: { security: 'reality', sni: 'a.com', flow: 'xtls-rprx-vision' },
      name: 'node 1',
    });
    const r = parseLink(link);
    expect(r.credential).toBe('uuid-abc');
    expect(r.address).toBe('example.com');
    expect(r.port).toBe(443);
    expect(r.params.sni).toBe('a.com');
    expect(r.name).toBe('node 1');
  });

  it('trojan round-trips', () => {
    const r = parseLink(buildTrojan({ credential: 'pw', address: 'h.com', port: 443, name: 'x' }));
    expect(r.credential).toBe('pw');
    expect(r.port).toBe(443);
  });

  it('ss round-trips', () => {
    const r = parseLink(
      buildShadowsocks({ method: 'chacha20-poly1305', password: 'p', address: 'h', port: 8388 }),
    );
    expect(r.credential).toBe('chacha20-poly1305:p');
  });
});

describe('parseLink — errors', () => {
  it('throws on unsupported scheme', () => {
    expect(() => parseLink('http://example.com')).toThrow(/Unsupported/);
  });
});

describe('parseLink — XHTTP with a split download endpoint', () => {
  const extra = JSON.stringify({
    mode: 'stream-up',
    downloadSettings: { address: 'download.example.com', port: 443, network: 'xhttp' },
  });
  const link =
    'vless://11111111-2222-3333-4444-555555555555@upload.example.com:443'
    + `?type=xhttp&security=tls&mode=stream-up&extra=${encodeURIComponent(extra)}#Split`;

  // This parser is transport-agnostic on purpose: it carries query params
  // through as-is, so the download endpoint survives without xhttp ever
  // being modelled here. Pinned because the panel has two other link
  // implementations that DO model it, and this one must not drift into a
  // third that quietly drops half the connection.
  it('carries the download endpoint through untouched', () => {
    const r = parseLink(link);
    expect(r.address).toBe('upload.example.com');
    expect(r.params.type).toBe('xhttp');

    const decoded = JSON.parse(r.params.extra) as {
      downloadSettings?: { address?: string; port?: number };
    };
    expect(decoded.downloadSettings?.address).toBe('download.example.com');
    expect(decoded.downloadSettings?.port).toBe(443);
  });
});
