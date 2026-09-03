import { describe, expect, it } from 'vitest';

import { genAnytlsLink, genTuicLink } from '@/lib/xray/inbound-link';
import type { Inbound } from '@/schemas/api/inbound';

/* The same two links are pinned in internal/cores/internal/singbox/share_test.go:
   the subscription serves the Go one, the panel copies this one. */
const base = { id: 1, remark: 'tuic-1', port: 8443, listen: '', enable: true } as unknown as Inbound;

describe('sing-box share links agree with the Go renderer', () => {
  it('tuic', () => {
    const inbound = { ...base, protocol: 'tuic', settings: { serverName: 'x.example', congestionControl: 'cubic', clients: [] } } as unknown as Inbound;
    expect(
      genTuicLink({ inbound, address: '1.2.3.4', port: 8443, remark: '', clientId: 'c0ffee00-0000-4000-8000-000000000001', clientPassword: 'secret-password-1', email: 'a@example.com' }),
    ).toBe('tuic://c0ffee00-0000-4000-8000-000000000001:secret-password-1@1.2.3.4:8443?alpn=h3&congestion_control=cubic&sni=x.example&udp_relay_mode=native#tuic-1-a%40example.com');
  });

  it('anytls', () => {
    const inbound = { ...base, protocol: 'anytls', settings: { clients: [] } } as unknown as Inbound;
    expect(
      genAnytlsLink({ inbound, address: '1.2.3.4', port: 8443, remark: '', clientPassword: 'secret-password-1', email: 'a@example.com' }),
    ).toBe('anytls://secret-password-1@1.2.3.4:8443?sni=1.2.3.4#tuic-1-a%40example.com');
  });
});
