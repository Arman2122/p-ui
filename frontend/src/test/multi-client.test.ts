import { describe, expect, it } from 'vitest';
import { supportsMultipleClients } from '@/lib/inbounds/multi-client';

describe('supportsMultipleClients', () => {
  it('follows the kind modules, so a multi-user core is never hidden from the client pickers', () => {
    for (const kind of ['ocserv', 'openvpn', 'ssh', 'tuic', 'anytls', 'mtproto', 'vless', 'shadowsocks', 'wgkernel']) {
      expect(supportsMultipleClients(kind), kind).toBe(true);
    }
    for (const kind of ['http', 'mixed', 'tunnel', 'tun', 'nope', undefined]) {
      expect(supportsMultipleClients(kind), String(kind)).toBe(false);
    }
  });
});
