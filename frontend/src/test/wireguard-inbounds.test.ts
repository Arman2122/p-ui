import { describe, expect, it } from 'vitest';

import { findWireguardInbound, findWireguardInbounds } from '@/pages/clients/wireguardConfig';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';

/*
A client on two WireGuard inbounds always got the first one's config; the
second interface's address was unreachable from the panel. The modals now
list every WireGuard-like inbound and let the operator pick. Feature gap N3.
*/
describe('findWireguardInbounds', () => {
  const inbounds: Record<number, InboundOption> = {
    1: { id: 1, tag: 'vless-a', protocol: 'vless' },
    2: { id: 2, tag: 'wg-home', protocol: 'wgkernel' },
    3: { id: 3, tag: 'awg-road', protocol: 'awgkernel' },
    4: { id: 4, tag: 'wg-xray', protocol: 'wireguard' },
  };
  const client = { email: 'a', inboundIds: [3, 1, 2, 9, 4] } as unknown as ClientRecord;

  it('lists every WireGuard-like inbound in attachment order and skips the rest', () => {
    expect(findWireguardInbounds(client, inbounds).map((ib) => ib.id)).toEqual([3, 2, 4]);
    expect(findWireguardInbound(client, inbounds)?.id).toBe(3);
  });

  it('is empty for a client with none', () => {
    expect(findWireguardInbounds({ email: 'b', inboundIds: [1] } as unknown as ClientRecord, inbounds)).toEqual([]);
    expect(findWireguardInbounds(null, inbounds)).toEqual([]);
  });
});
