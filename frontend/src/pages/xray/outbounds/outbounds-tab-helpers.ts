import type { TFunction } from 'i18next';

import { OutboundProtocols as Protocols } from '@/schemas/primitives';
import { isUdpOutbound } from '@/hooks/useXraySetting';
import type { OutboundTestMode, OutboundTestState, OutboundTrafficRow } from '@/hooks/useXraySetting';

import { EGRESS_TYPE, EGRESS_TYPE_UPLINK, UplinkSettingsSchema, exitKeyFor, type EgressRecord } from '@/schemas/api/egress';

import type { OutboundRow } from './outbounds-tab-types';
import type { XraySettingsValue } from '@/hooks/useXraySetting';
import type { RoutingRuleRecord } from '@/schemas/api/routing';

export function isBuiltinOutbound(row: OutboundRow): boolean {
  return !row.egress && ((row.tag === 'direct' && row.protocol === Protocols.Freedom)
    || (row.tag === 'blocked' && row.protocol === Protocols.Blackhole) || row.tag === 'api');
}

export function observedTraffic(traffic: OutboundTrafficRow[], row: OutboundRow): OutboundTrafficRow | undefined {
  return row.egress ? undefined : traffic.find((item) => item.tag === row.tag);
}

export function referencedRules(template: XraySettingsValue | null, row: OutboundRow, hostRules?: RoutingRuleRecord[]): string[] | undefined {
  if (!hostRules) return undefined;
  if (row.egress) return hostRules?.filter((rule) => rule.destKind === 'exit' && rule.destExitId === row.egress?.id)
    .map((rule) => rule.remark || `#${rule.id}`);
  const tag = row.tag;
  if (!tag || !template) return undefined;
  const balancers = new Set((template.routing?.balancers ?? []).filter((balancer) =>
    balancer.fallbackTag === tag || balancer.selector.some((prefix) => tag.startsWith(prefix))).map((balancer) => balancer.tag));
  const refs = (template.routing?.rules ?? []).flatMap((rule, index) =>
    rule.outboundTag === tag || (rule.balancerTag && balancers.has(rule.balancerTag)) ? [rule.ruleTag || `#${index + 1}`] : []);
  for (const rule of hostRules ?? []) {
    if ((rule.destKind === 'outbound' && rule.destTag === tag)
      || (rule.destKind === 'balancer' && balancers.has(rule.destTag))
      || (rule.destKind === 'direct' && tag === 'direct') || (rule.destKind === 'block' && tag === 'blocked')) {
      refs.push(rule.remark || `#${rule.id}`);
    }
  }
  return refs;
}

/**
 * Translate a table row's positional index into that outbound's index in the
 * full, unfiltered outbounds array. The table hides balancer-loopback outbounds
 * but keeps each visible row's original index in `key`, so any handler that
 * mutates the outbounds array (or probes an outbound by index) must map the
 * positional index back through `key` or it operates on the wrong outbound once
 * a hidden loopback precedes it.
 */
export function originalOutboundIndex(rows: OutboundRow[], positionalIndex: number): number {
  const row = rows[positionalIndex];
  return row ? row.key : positionalIndex;
}

/* An exit's address is where it dials, which for an uplink is its endpoint and
   for a front is the outbound tag it hands traffic to. */
export function exitAddress(row: EgressRecord): string {
  if (row.target) return row.target;
  try {
    const parsed = UplinkSettingsSchema.safeParse(JSON.parse(row.settings || '{}'));
    if (parsed.success && parsed.data.endpoint) return parsed.data.endpoint;
    const settings = JSON.parse(row.settings || '{}') as Record<string, unknown>;
    if (typeof settings.host === 'string') return `${settings.host}${settings.port ? `:${settings.port}` : ''}`;
    if (typeof settings.server === 'string') return settings.server;
    if (typeof settings.profile === 'string') return settings.profile.match(/^\s*remote\s+(\S+)(?:\s+(\d+))?/m)?.slice(1).filter(Boolean).join(':') ?? '';
    return '';
  } catch {
    return '';
  }
}

/* An exit rendered as a table row. The protocol reads as what an operator chose
   in the picker, not as the driver's type string. */
export function exitAsOutboundRow(row: EgressRecord): OutboundRow {
  return {
    key: exitKeyFor(row.id),
    tag: row.remark || `exit #${row.id}`,
    protocol: row.type === EGRESS_TYPE_UPLINK ? 'wireguard (kernel)' : row.type,
    egress: row,
  };
}

export function outboundAddresses(o: OutboundRow): string[] {
  if (o.egress) {
    const where = exitAddress(o.egress);
    return where ? [where] : [];
  }
  const settings = o.settings as Record<string, unknown> | undefined;
  switch (o.protocol) {
    case Protocols.VMess: {
      const serverObj = settings?.vnext as Array<{ address: string; port: number }> | undefined;
      return serverObj ? serverObj.map((s) => `${s.address}:${s.port}`) : [];
    }
    case Protocols.VLESS:
    case Protocols.Hysteria:
      return settings?.address ? [`${settings.address}${settings.port ? `:${settings.port}` : ''}`] : [];
    case Protocols.HTTP:
    case Protocols.Socks:
    case Protocols.Shadowsocks:
    case Protocols.Trojan: {
      const serverObj = settings?.servers as Array<{ address: string; port: number }> | undefined;
      return serverObj ? serverObj.map((s) => `${s.address}:${s.port}`) : [];
    }
    case Protocols.DNS: {
      const addr = (settings?.rewriteAddress as string) || (settings?.address as string) || '';
      const port = (settings?.rewritePort as string | number) || (settings?.port as string | number) || '';
      return addr || port ? [`${addr}:${port}`] : [];
    }
    case Protocols.Wireguard:
      return (((settings?.peers as Array<{ endpoint?: string }>) || []).map((p) => p.endpoint || '').filter(Boolean));
    case Protocols.Freedom:
      return typeof settings?.redirect === 'string' && settings.redirect ? [settings.redirect] : [];
    case Protocols.Loopback:
      return typeof settings?.inboundTag === 'string' && settings.inboundTag ? [settings.inboundTag] : [];
    default:
      return [];
  }
}

export function isUntestable(o: OutboundRow): boolean {
  if (!o) return true;
  // A disabled exit installs nothing, and a front is measured by testing the
  // outbound it targets — the backend refuses both rather than timing them.
  if (o.egress) return !o.egress.enable || o.egress.type === EGRESS_TYPE;
  if (o.protocol === Protocols.Blackhole || o.protocol === Protocols.Loopback || o.tag === 'blocked') return true;
  // freedom ("direct") and dns aren't proxies — a TCP dial has no endpoint and
  // an HTTP probe would only measure the host's own direct reachability, so
  // they're untestable in every mode.
  if (o.protocol === Protocols.Freedom || o.protocol === Protocols.DNS) return true;
  return false;
}

export function showSecurity(security?: string): boolean {
  return security === 'tls' || security === 'reality';
}

export function effectiveTestMode(o: unknown, mode: OutboundTestMode): OutboundTestMode {
  return mode === 'tcp' && isUdpOutbound(o) ? 'http' : mode;
}

export function testModeLabel(mode: string, t: TFunction): string {
  return mode === 'real' ? t('pages.xray.outbound.modeRealDelay') : mode.toUpperCase();
}

export function trafficFor(outboundsTraffic: OutboundTrafficRow[], o: OutboundRow): { up: number; down: number } {
  const tr = outboundsTraffic.find((x) => x.tag === o.tag);
  return { up: tr?.up || 0, down: tr?.down || 0 };
}

export function countryFlag(country?: string): string {
  const code = (country || '').trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return '';
  return String.fromCodePoint(...[...code].map((ch) => 0x1f1e6 + ch.charCodeAt(0) - 65));
}

export function countryName(country?: string, locale?: string): string {
  const code = (country || '').trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return '';
  try {
    return new Intl.DisplayNames(locale ? [locale] : undefined, { type: 'region' }).of(code) || code;
  } catch {
    return code;
  }
}

export function isTesting<K extends string | number>(states: Record<K, OutboundTestState>, idx: K): boolean {
  return !!states?.[idx]?.testing;
}

export function testResult<K extends string | number>(states: Record<K, OutboundTestState>, idx: K) {
  return states?.[idx]?.result || null;
}

/**
 * Tags that must not be offered as this outbound's dialerProxy, because
 * choosing one closes a loop.
 *
 * dialerProxy makes an outbound dial THROUGH another, so a chain that comes
 * back to the outbound being edited has no bottom: xray follows it until the
 * dial gives up, and the outbound is simply dead with nothing said. Excluding
 * only the row being edited catches the one-step loop and nothing longer.
 */
export function dialerProxyCycleTags(
  outbounds: Array<{ tag?: string; streamSettings?: unknown } | null | undefined>,
  editingTag: string,
): Set<string> {
  const unsafe = new Set<string>();
  if (!editingTag) return unsafe;
  unsafe.add(editingTag);

  const dialsThrough = new Map<string, string>();
  for (const outbound of outbounds) {
    const tag = outbound?.tag;
    const via = dialerProxyOf(outbound?.streamSettings);
    if (tag && via) dialsThrough.set(tag, via);
  }

  for (const [tag] of dialsThrough) {
    const walked = new Set<string>([tag]);
    let at: string | undefined = dialsThrough.get(tag);
    while (at) {
      if (at === editingTag) {
        unsafe.add(tag);
        break;
      }
      if (walked.has(at)) break;
      walked.add(at);
      at = dialsThrough.get(at);
    }
  }
  return unsafe;
}

/* streamSettings is carried as unknown through the template, and a dialerProxy
   is only ever read for this comparison. */
function dialerProxyOf(streamSettings: unknown): string {
  const sockopt = (streamSettings as { sockopt?: unknown } | null | undefined)?.sockopt;
  const via = (sockopt as { dialerProxy?: unknown } | null | undefined)?.dialerProxy;
  return typeof via === 'string' ? via : '';
}
