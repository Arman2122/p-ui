import { describe, expect, it } from 'vitest';

import { fallbackDefaultsFromChild } from '@/pages/inbounds/form/useInboundFallbacks';
import type { DBInbound } from '@/models/dbinbound';

/*
The Fallbacks card fills a row in from the child inbound it points at, and the
path it filled in for a gRPC child was the bare `serviceName`.

xray-core refuses a non-empty fallback path that does not start with "/" —
infra/conf/vless.go:185, trojan.go:175 — and that refusal fails the WHOLE
config, so every inbound of every protocol stops serving. So this was not an
operator typo: two clicks in the form wrote a value that stops the box coming
back up on its next cold start. F326.

The gRPC request path really is /{serviceName}/{method}, so the slash is what
the core wants to match on as well as what it requires.
*/

function child(streamSettings: Record<string, unknown>): DBInbound {
  return {
    id: 7,
    protocol: 'vless',
    port: 8443,
    listen: '',
    remark: 'child',
    settings: '{}',
    streamSettings: JSON.stringify(streamSettings),
    sniffing: '{}',
  } as unknown as DBInbound;
}

describe('the path a fallback row is filled in with', () => {
  it('gives a gRPC serviceName the slash the core requires', () => {
    const got = fallbackDefaultsFromChild(child({
      network: 'grpc',
      grpcSettings: { serviceName: 'GunService' },
    }));
    expect(got.path).toBe('/GunService');
  });

  it('does not double the slash on a serviceName that already has one', () => {
    const got = fallbackDefaultsFromChild(child({
      network: 'grpc',
      grpcSettings: { serviceName: '/GunService' },
    }));
    expect(got.path).toBe('/GunService');
  });

  it('leaves a gRPC child with no serviceName empty rather than a bare slash', () => {
    const got = fallbackDefaultsFromChild(child({ network: 'grpc', grpcSettings: {} }));
    expect(got.path).toBe('');
  });

  /* These already carried their slash, and are here so the switch above cannot
     start rewriting them. */
  it.each([
    ['ws', 'wsSettings', 'path', '/socket'],
    ['httpupgrade', 'httpupgradeSettings', 'path', '/upgrade'],
    ['xhttp', 'xhttpSettings', 'path', '/x'],
  ])('passes a %s path through unchanged', (network, key, field, value) => {
    const got = fallbackDefaultsFromChild(child({ network, [key]: { [field]: value } }));
    expect(got.path).toBe(value);
  });

  it('leaves a transport with no path of its own empty', () => {
    expect(fallbackDefaultsFromChild(child({ network: 'tcp' })).path).toBe('');
    expect(fallbackDefaultsFromChild(child({ network: 'kcp' })).path).toBe('');
  });

  // The fields beside the path must not have moved.
  it('still reads the SNI and ALPN off the child TLS block', () => {
    const got = fallbackDefaultsFromChild(child({
      network: 'ws',
      wsSettings: { path: '/s' },
      tlsSettings: { serverName: 'child.example', alpn: ['h2', 'http/1.1'] },
    }));
    expect(got.name).toBe('child.example');
    expect(got.alpn).toBe('h2,http/1.1');
    expect(got.xver).toBe(0);
  });
});
