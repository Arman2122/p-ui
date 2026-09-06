import type { z } from 'zod';

import type { AnyInboundSettings } from '@/lib/xray/inbound-defaults';
import type { Protocol } from '@/schemas/primitives/protocol';

import { anytls } from './anytls';
import { awgkernel } from './awgkernel';
import { http } from './http';
import { hysteria } from './hysteria';
import { mixed } from './mixed';
import { mtproto } from './mtproto';
import { ocserv } from './ocserv';
import { openvpn } from './openvpn';
import { shadowsocks } from './shadowsocks';
import { ssh } from './ssh';
import { trojan } from './trojan';
import { tuic } from './tuic';
import { tun } from './tun';
import { tunnel } from './tunnel';
import { vless } from './vless';
import { vmess } from './vmess';
import { wgkernel } from './wgkernel';
import { wireguard } from './wireguard';
import { NONE, TCP, UDP, type KindModule, type StreamMode, type TransportBits } from './module';

export { NONE, TCP, UDP, type KindModule, type StreamMode, type TransportBits };

export const KIND_MODULES: Record<Protocol, KindModule> = {
  vless,
  vmess,
  trojan,
  shadowsocks,
  hysteria,
  http,
  mixed,
  tunnel,
  tun,
  wireguard,
  wgkernel,
  awgkernel,
  mtproto,
  ssh,
  tuic,
  anytls,
  openvpn,
  ocserv,
};

/* undefined for a kind this build does not know, which an older panel meets
   when a newer master created the inbound. */
export function kindModule(kind: string): KindModule | undefined {
  return (KIND_MODULES as Record<string, KindModule | undefined>)[kind];
}

export function createDefaultInboundSettings(kind: string): AnyInboundSettings | null {
  return kindModule(kind)?.defaultSettings() ?? null;
}

export function clientSchemaFor(kind: string): z.ZodType | null {
  return kindModule(kind)?.clientSchema ?? null;
}

export function kindTransports(
  kind: string,
  settings: Record<string, unknown> | undefined,
): TransportBits | undefined {
  return kindModule(kind)?.transports?.(settings);
}

export function isMultiUserKind(kind: string, settings: { method?: string }): boolean {
  const rule = kindModule(kind)?.multiUser ?? false;
  return typeof rule === 'function' ? rule(settings) : rule;
}

export function hasShareLink(kind: string): boolean {
  return kindModule(kind)?.shareLink ?? false;
}

export function streamModeOf(kind: string): StreamMode {
  return kindModule(kind)?.stream ?? 'selectable';
}
