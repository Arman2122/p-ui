import type { CoreStatusView, CoreView } from '@/generated/zod';
import { CapabilitiesSchema } from '@/generated/zod';

export function corePageFixtures(now = Date.now()): CoreStatusView[] {
  const base = { available: true, state: 'running', ready: true, instances: [], logs: true };
  return [
    { ...base, id: 'xray', titleKey: 'cores.xray.title', pid: 4501, since: now - 86400000, version: { installed: '26.7.28', reported: true, manageable: true }, trafficSeenAt: now - 1000 },
    { ...base, id: 'singbox', titleKey: 'cores.singbox.title', version: { installed: '1.12.0', reported: true, manageable: true }, instances: [{ instanceId: 11, tag: 'singbox-europe', state: 'running', ready: true, pid: 4502, restarts: 2, since: now - 3600000 }] },
    { ...base, id: 'wgkernel', titleKey: 'cores.wgkernel.title', logs: false, version: { installed: '6.8.0', reported: true, manageable: false }, instances: [{ instanceId: 12, tag: 'wg-office', state: 'running', ready: true }] },
    { ...base, id: 'awgkernel', titleKey: 'cores.awgkernel.title', state: 'idle', ready: false, logs: false, version: { installed: '1.0.20250920', reported: true, manageable: false } },
    { ...base, id: 'ssh', titleKey: 'cores.ssh.title', version: { reported: false, manageable: false }, instances: [{ instanceId: 13, tag: 'ssh-local', state: 'running', ready: true, pid: 4400, since: now - 7200000 }] },
    { ...base, id: 'mtproto', titleKey: 'cores.mtproto.title', state: 'idle', ready: false, available: false, unavailable: 'mtg-proxy 1.0.1 is installed; version 1.9.0 or later is required.', version: { installed: '1.0.1', reported: true, manageable: true } },
    { ...base, id: 'openvpn', titleKey: 'cores.openvpn.title', state: 'stopped', ready: false, version: { installed: '2.6.12', reported: true, manageable: false }, instances: [{ instanceId: 14, tag: 'openvpn-home', state: 'stopped', ready: false, detail: 'Stopped by operator' }] },
    { ...base, id: 'ocserv', titleKey: 'cores.ocserv.title', state: 'degraded', ready: false, detail: 'Management socket is not answering', version: { installed: '1.3.0', reported: true, manageable: false }, instances: [{ instanceId: 15, tag: 'office-connect', state: 'running', ready: false, pid: 4510, restarts: 1 }] },
  ];
}

export function coreDescriptorFixtures(): CoreView[] {
  const kinds: Record<string, string[]> = { xray: ['vless', 'vmess', 'trojan'], singbox: ['anytls', 'tuic'], wgkernel: ['wgkernel'], awgkernel: ['awgkernel'], ssh: ['ssh'], mtproto: ['mtproto'], openvpn: ['openvpn'], ocserv: ['ocserv'] };
  const caps = CapabilitiesSchema.parse(Object.fromEntries(Object.keys(CapabilitiesSchema.shape).map((key) => [key, false])));
  return corePageFixtures().map((core) => ({ id: core.id, titleKey: core.titleKey, kinds: kinds[core.id], available: core.available, unavailable: core.unavailable || '', caps,
    applyDelaySeconds: {}, clientCredentials: {}, exitKinds: [], portlessKinds: [], rateKinds: [], sessionKinds: [], shaping: {} }));
}

export const CORE_INBOUND_FIXTURES = [
  { id: 10, remark: 'Edge VLESS', protocol: 'vless', port: 443 },
  { id: 11, remark: 'Europe', protocol: 'anytls', port: 8443 },
  { id: 12, remark: 'Office WireGuard', protocol: 'wgkernel', port: 51820 },
  { id: 13, remark: 'Private SSH', protocol: 'ssh', port: 2222 },
  { id: 14, remark: 'Home VPN', protocol: 'openvpn', port: 1194 },
  { id: 15, remark: 'Office Connect', protocol: 'ocserv', port: 4443 },
];
