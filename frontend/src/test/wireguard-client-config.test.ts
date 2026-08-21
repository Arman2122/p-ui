import { describe, expect, it } from 'vitest';

import { findWireguardInbound } from '@/pages/clients/wireguardConfig';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';

const client: ClientRecord = {
  email: 'alice',
  privateKey: 'QGVlb2dXc1ZTWGw0ZXBzZndsWmtMaUM5MUlNYjBHWFdYbz0=',
  allowedIPs: '10.0.0.2/32',
  preSharedKey: 'cHNrLXZhbHVlLWZvci13aXJlZ3VhcmQtdGVzdC1jYXNlIQ==',
  keepAlive: 25,
  inboundIds: [90],
};

/*
The .conf assertions that used to live here moved to the server with the
builder they described: internal/cores/internal/wireguard/share_test.go for the
file itself, internal/cores/internal/amneziawg/share_test.go for the
obfuscation, internal/sub for the endpoint host. The page fetches that file now
rather than assembling a second one out of fields it does not have.
*/
const inbound: InboundOption = {
  id: 90,
  tag: 'in-51820-udp',
  remark: 'wg-mc',
  protocol: 'wireguard',
  port: 51820,
  wgPublicKey: 'DGSYIcEKAUkA7HhzGSjxLZuV67BR3LeyU0BMLJzNVHQ=',
  wgMtu: 1420,
};

/* The client card asks this which of its inbounds owns the tunnel. Two cores
   answer to it now, so a check on either string alone hides the other's .conf. */
describe('findWireguardInbound', () => {
  const vless: InboundOption = { ...inbound, id: 88, protocol: 'vless' };
  const cases: [string, string, number | undefined][] = [
    ['xray wireguard', 'wireguard', 90],
    ['kernel wgkernel', 'wgkernel', 90],
    ['neither', 'trojan', undefined],
  ];

  for (const [name, protocol, want] of cases) {
    it(`finds the ${name} inbound`, () => {
      const tunnel: InboundOption = { ...inbound, protocol };
      const found = findWireguardInbound(
        { ...client, inboundIds: [88, 90] },
        { 88: vless, 90: tunnel },
      );
      expect(found?.id).toBe(want);
    });
  }
});

