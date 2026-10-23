import { describe, expect, it } from 'vitest';

import { formValuesToWirePayload, rawOutboundToFormValues } from '@/lib/xray/outbound-form-adapter';
import { normalizeStreamSettingsForWire } from '@/lib/xray/stream-wire-normalize';
import { createTlsSettingsWithDefaultCert } from '@/lib/xray/inbound-tls-defaults';
import { TlsStreamSettingsSchema } from '@/schemas/protocols/security/tls';
import { parseVmessLink } from '@/lib/xray/outbound-link-parser';
import { setDirectFreedomStrategy } from '@/pages/xray/basics/helpers';
import { XraySettingsValueSchema } from '@/schemas/xray';
import { Base64 } from '@/utils';

describe('upstream config compatibility', () => {
  it('keeps explicit uTLS None after serializing and reopening an inbound', () => {
    const wire = normalizeStreamSettingsForWire({ network: 'hysteria', security: 'tls', tlsSettings: { settings: { fingerprint: '' } } }, { side: 'inbound' });
    expect(TlsStreamSettingsSchema.parse(wire.tlsSettings).settings.fingerprint).toBe('');
    expect(createTlsSettingsWithDefaultCert()).toMatchObject({ settings: { fingerprint: 'chrome' } });
  });

  it.each([
    { settings: { domainStrategy: 'UseIPv4' } },
    { settings: { targetStrategy: 'UseIPv4' } },
    { settings: {}, targetStrategy: 'UseIPv4' },
    { settings: {}, streamSettings: { sockopt: { domainStrategy: 'UseIPv4' } } },
  ])('saves a freedom strategy where the running core resolves domains: %j', (legacy) => {
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ protocol: 'freedom', ...legacy }));
    expect(wire).toMatchObject({ streamSettings: { sockopt: { domainStrategy: 'UseIPv4' } } });
    expect(wire).not.toHaveProperty('targetStrategy');
    expect(wire.settings).not.toHaveProperty('domainStrategy');
    expect(wire.settings).not.toHaveProperty('targetStrategy');
  });

  it('does not widen DNS qType zero into a rule matching every query', () => {
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ protocol: 'dns', settings: { rules: [{ action: 'drop', qType: 0 }] } }));
    expect(wire.settings).toMatchObject({ rules: [{ action: 'drop', qType: '0' }] });
  });

  it('reads outbound protocol casing the way the core does', () => {
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ protocol: 'ShAdOwSoCkS', settings: { servers: [{ address: 'example.com', port: 8443, method: 'aes-128-gcm', password: 'secret' }] } }));
    expect(wire).toMatchObject({ protocol: 'shadowsocks', settings: { servers: [{ address: 'example.com', port: 8443, password: 'secret' }] } });
  });

  it('preserves certificate checks through the outbound form after importing VMess', () => {
    const parsed = parseVmessLink(`vmess://${Base64.encode(JSON.stringify({
      add: 'example.com', port: 443, id: '11111111-2222-4333-8444-555555555555', net: 'ws', tls: 'tls',
      ech: 'ech-config', pcs: 'pin-one,pin-two', vcn: 'certificate.example', sni: 'front.example',
    }))}`);
    if (!parsed) throw new Error('VMess import failed');
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ ...parsed, protocol: 'vmess' }));
    expect(wire.streamSettings).toMatchObject({ tlsSettings: {
      echConfigList: 'ech-config', pinnedPeerCertSha256: 'pin-one,pin-two', verifyPeerCertByName: 'certificate.example', serverName: 'front.example',
    } });
  });

  it('keeps the effective legacy freedom strategy and unrelated socket options on save', () => {
    const wire = formValuesToWirePayload(rawOutboundToFormValues({ protocol: 'freedom',
      targetStrategy: 'ForceIPv4', settings: { targetStrategy: 'UseIPv6', domainStrategy: 'UseIP' },
      streamSettings: { sockopt: { domainStrategy: 'UseIPv4', mark: 17, dialerProxy: 'front' } },
    }));
    expect(wire.streamSettings).toEqual({ sockopt: { domainStrategy: 'ForceIPv4', mark: 17, dialerProxy: 'front' } });
    expect(wire).not.toHaveProperty('targetStrategy');
    expect(wire.settings).not.toHaveProperty('domainStrategy');
    expect(wire.settings).not.toHaveProperty('targetStrategy');
  });

  it('changes the direct strategy without leaving a legacy value to override it', () => {
    const settings = XraySettingsValueSchema.parse({ outbounds: [{ protocol: 'Freedom', tag: 'direct',
      targetStrategy: 'ForceIPv4', settings: { targetStrategy: 'UseIPv6', domainStrategy: 'UseIP', userLevel: 4 },
      streamSettings: { sockopt: { domainStrategy: 'UseIPv4', mark: 17 } },
    }] });
    setDirectFreedomStrategy(settings, 'UseIPv6');
    expect(settings.outbounds).toEqual([{ protocol: 'Freedom', tag: 'direct', settings: { userLevel: 4 }, streamSettings: { sockopt: { domainStrategy: 'UseIPv6', mark: 17 } } }]);
    setDirectFreedomStrategy(settings, 'AsIs');
    expect(settings.outbounds?.[0]?.streamSettings).toEqual({ sockopt: { mark: 17 } });
  });
});
