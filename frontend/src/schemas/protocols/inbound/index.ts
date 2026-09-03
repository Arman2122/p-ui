import { z } from 'zod';

import { HttpInboundSettingsSchema } from './http';
import { HysteriaInboundSettingsSchema } from './hysteria';
import { MixedInboundSettingsSchema } from './mixed';
import { MtprotoInboundSettingsSchema } from './mtproto';
import { SshInboundSettingsSchema } from './ssh';
import { TuicInboundSettingsSchema } from './tuic';
import { AnytlsInboundSettingsSchema } from './anytls';
import { OcservInboundSettingsSchema } from './ocserv';
import { OpenvpnInboundSettingsSchema } from './openvpn';
import { ShadowsocksInboundSettingsSchema } from './shadowsocks';
import { TrojanInboundSettingsSchema } from './trojan';
import { TunInboundSettingsSchema } from './tun';
import { TunnelInboundSettingsSchema } from './tunnel';
import { VlessInboundSettingsSchema } from './vless';
import { VmessInboundSettingsSchema } from './vmess';
import { WgkernelInboundSettingsSchema } from './wgkernel';
import { AwgkernelInboundSettingsSchema } from './awgkernel';
import { WireguardInboundSettingsSchema } from './wireguard';

export * from './http';
export * from './hysteria';
export * from './mixed';
export * from './mtproto';
export * from './ssh';
export * from './tuic';
export * from './anytls';
export * from './ocserv';
export * from './openvpn';
export * from './shadowsocks';
export * from './trojan';
export * from './tun';
export * from './tunnel';
export * from './vless';
export * from './vmess';
export * from './wgkernel';
export * from './awgkernel';
export * from './wireguard';

// Tagged-wrapper discriminated union. The discriminator (`protocol`) lives on
// the wrapper, not inside `settings`, mirroring the wire format Xray emits:
//   { protocol: 'vless', settings: { clients: [...], ... }, ... }
// Consumers narrow on `.protocol` and TypeScript narrows `.settings` to the
// matching leaf type.
export const InboundSettingsSchema = z.discriminatedUnion('protocol', [
  z.object({ protocol: z.literal('vmess'),       settings: VmessInboundSettingsSchema }),
  z.object({ protocol: z.literal('vless'),       settings: VlessInboundSettingsSchema }),
  z.object({ protocol: z.literal('trojan'),      settings: TrojanInboundSettingsSchema }),
  z.object({ protocol: z.literal('shadowsocks'), settings: ShadowsocksInboundSettingsSchema }),
  z.object({ protocol: z.literal('wireguard'),   settings: WireguardInboundSettingsSchema }),
  z.object({ protocol: z.literal('wgkernel'),    settings: WgkernelInboundSettingsSchema }),
  z.object({ protocol: z.literal('awgkernel'),   settings: AwgkernelInboundSettingsSchema }),
  z.object({ protocol: z.literal('hysteria'),    settings: HysteriaInboundSettingsSchema }),
  z.object({ protocol: z.literal('http'),        settings: HttpInboundSettingsSchema }),
  z.object({ protocol: z.literal('mixed'),       settings: MixedInboundSettingsSchema }),
  z.object({ protocol: z.literal('tunnel'),      settings: TunnelInboundSettingsSchema }),
  z.object({ protocol: z.literal('tun'),         settings: TunInboundSettingsSchema }),
  z.object({ protocol: z.literal('mtproto'),     settings: MtprotoInboundSettingsSchema }),
  z.object({ protocol: z.literal('ssh'),         settings: SshInboundSettingsSchema }),
  z.object({ protocol: z.literal('tuic'),        settings: TuicInboundSettingsSchema }),
  z.object({ protocol: z.literal('anytls'),      settings: AnytlsInboundSettingsSchema }),
  z.object({ protocol: z.literal('openvpn'),     settings: OpenvpnInboundSettingsSchema }),
  z.object({ protocol: z.literal('ocserv'),      settings: OcservInboundSettingsSchema }),
]);
export type InboundSettings = z.infer<typeof InboundSettingsSchema>;
