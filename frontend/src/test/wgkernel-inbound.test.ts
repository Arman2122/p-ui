import { describe, expect, it } from 'vitest';

import { genInboundLinks, genWireguardConfigs, genWireguardLinks } from '@/lib/xray/inbound-link';
import { createDefaultInboundSettings } from '@/lib/xray/inbound-defaults';
import { InboundSchema } from '@/schemas/api/inbound';
import { InboundSettingsSchema } from '@/schemas/protocols/inbound';
import { WgkernelInboundSettingsSchema } from '@/schemas/protocols/inbound/wgkernel';

const SERVER_SECRET = 'iJ2cBkrSGqRwIfYIDIxk7hr5RXfdR93MfJUL7yqkkH8=';

/* For kernel WireGuard the .conf IS the product, so every renderer that skips
   the kind hands the subscriber nothing. wgkernel carries no `peers` key at all
   — only `clients` — which is what the peers fallback below has to survive. */
function wgkernelInbound(clients: unknown[]) {
  return InboundSchema.parse({
    id: 92,
    remark: 'wgk',
    port: 51820,
    protocol: 'wgkernel',
    settings: {
      address: ['10.0.0.1/24'],
      secretKey: SERVER_SECRET,
      mtu: 1420,
      clients,
    },
  });
}

const ALICE = {
  email: 'alice',
  privateKey: 'QGVlb2dXc1ZTWGw0ZXBzZndsWmtMaUM5MUlNYjBHWFdYbz0=',
  publicKey: 'DGSYIcEKAUkA7HhzGSjxLZuV67BR3LeyU0BMLJzNVHQ=',
  allowedIPs: ['10.0.0.2/32'],
  keepAlive: 25,
};

describe('wgkernel inbound settings schema', () => {
  it('accepts what the add form seeds, so a new inbound is saveable', () => {
    const settings = createDefaultInboundSettings('wgkernel');
    const parsed = InboundSettingsSchema.safeParse({ protocol: 'wgkernel', settings });
    expect(parsed.success).toBe(true);
    expect(settings).toMatchObject({ address: ['10.0.0.1/24'] });
  });

  it('requires an interface address — a device with none routes nothing back', () => {
    const parsed = WgkernelInboundSettingsSchema.safeParse({
      address: [],
      secretKey: SERVER_SECRET,
    });
    expect(parsed.success).toBe(false);
  });

  it('drops the xray-only TUN fields instead of persisting them', () => {
    const parsed = WgkernelInboundSettingsSchema.parse({
      address: ['10.0.0.1/24'],
      secretKey: SERVER_SECRET,
      noKernelTun: true,
      domainStrategy: 'ForceIPv4',
    });
    expect(parsed).not.toHaveProperty('noKernelTun');
    expect(parsed).not.toHaveProperty('domainStrategy');
  });
});

describe('wgkernel link and config fan-out', () => {
  it('emits a .conf and a link per client', () => {
    const inbound = wgkernelInbound([ALICE]);
    const configs = genWireguardConfigs({ inbound, remark: 'wgk', fallbackHostname: 'wgk.example.test' });
    expect(configs).toContain('Address = 10.0.0.2/32');
    expect(configs).toContain('PersistentKeepalive = 25');

    const links = genWireguardLinks({ inbound, remark: 'wgk', fallbackHostname: 'wgk.example.test' });
    expect(links).toContain('wireguard://');
    expect(links).toContain('address=10.0.0.2%2F32');
  });

  it('routes the export-links action to the .conf block, not to an empty string', () => {
    const out = genInboundLinks({
      inbound: wgkernelInbound([ALICE]),
      remark: 'wgk',
      fallbackHostname: 'wgk.example.test',
    });
    expect(out).toContain('[Interface]');
  });

  it('renders nothing rather than throwing for an inbound with no clients yet', () => {
    const inbound = wgkernelInbound([]);
    expect(genWireguardConfigs({ inbound, remark: 'wgk', fallbackHostname: 'wgk.example.test' })).toBe('');
    expect(genWireguardLinks({ inbound, remark: 'wgk', fallbackHostname: 'wgk.example.test' })).toBe('');
  });
});
