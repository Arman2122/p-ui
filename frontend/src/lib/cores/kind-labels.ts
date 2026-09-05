import type { Protocol } from '@/schemas/primitives/protocol';

/*
What an operator reads for each inbound kind, and where it sits in the picker.
The kind itself stays the value everywhere else (tags, links, the API), so a
name here changes nothing on the wire. A kind missing from KIND_NAME_KEYS is a
compile error; kind-labels.test.ts fails a kind that no group lists.
*/
export const KIND_NAME_KEYS: Record<Protocol, string> = {
  vless: 'protocols.names.vless',
  vmess: 'protocols.names.vmess',
  trojan: 'protocols.names.trojan',
  shadowsocks: 'protocols.names.shadowsocks',
  hysteria: 'protocols.names.hysteria',
  http: 'protocols.names.http',
  mixed: 'protocols.names.mixed',
  tunnel: 'protocols.names.tunnel',
  tun: 'protocols.names.tun',
  wireguard: 'protocols.names.wireguard',
  tuic: 'protocols.names.tuic',
  anytls: 'protocols.names.anytls',
  wgkernel: 'protocols.names.wgkernel',
  awgkernel: 'protocols.names.awgkernel',
  openvpn: 'protocols.names.openvpn',
  ocserv: 'protocols.names.ocserv',
  ssh: 'protocols.names.ssh',
  mtproto: 'protocols.names.mtproto',
};

export interface KindGroup {
  key: string;
  titleKey: string;
  kinds: readonly Protocol[];
}

/* Grouped by what runs them, in the order the picker shows. */
export const KIND_GROUPS: readonly KindGroup[] = [
  {
    key: 'xray',
    titleKey: 'protocols.groups.xray',
    kinds: ['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria', 'http', 'mixed', 'tunnel', 'tun', 'wireguard'],
  },
  { key: 'singbox', titleKey: 'protocols.groups.singbox', kinds: ['tuic', 'anytls'] },
  { key: 'vpn', titleKey: 'protocols.groups.vpn', kinds: ['wgkernel', 'awgkernel', 'openvpn', 'ocserv'] },
  { key: 'tunnels', titleKey: 'protocols.groups.tunnels', kinds: ['ssh', 'mtproto'] },
];

type Translate = (key: string) => string;

/* A kind this build does not know keeps its raw id, so an older panel never
   shows an empty tag for an inbound a newer master created. */
export function kindLabel(t: Translate, kind: string): string {
  const key = (KIND_NAME_KEYS as Record<string, string | undefined>)[kind];
  return key ? t(key) : kind;
}
