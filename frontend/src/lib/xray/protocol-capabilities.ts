import { can, type CapabilityFacts } from '@/lib/cores/capabilities';

/*
Capability predicates for inbound/outbound forms.

The rules used to live here as hand-written TypeScript, duplicated in
internal/web/service/inbound_protocol.go and internal/sub/service.go with
comments in each pointing at the others and no test crossing the boundary. Two of
them (canEnableTls, canEnableReality) existed ONLY here, so the REST API and the
Telegram bot could create configurations this file refuses.

They now come from one table generated out of internal/core/capability.go. These
wrappers stay because callers pass form-shaped slices, not facts.
*/

const VISION_FLOW = 'xtls-rprx-vision';
const SS_BLAKE3_CHACHA20 = '2022-blake3-chacha20-poly1305';

export interface CapabilityProtocolSlice {
  protocol: string;
  settings?: { encryption?: string; decryption?: string };
  streamSettings?: { network?: string; security?: string };
}

export interface CapabilityVlessSlice extends CapabilityProtocolSlice {
  settings?: { encryption?: string; decryption?: string; clients?: { flow?: string }[] };
}

export interface CapabilityShadowsocksSlice extends CapabilityProtocolSlice {
  settings?: { encryption?: string; method?: string };
}

/* Widest shape toFacts reads; each exported predicate narrows to its own slice. */
interface FactsInput {
  protocol: string;
  settings?: { encryption?: string; decryption?: string; method?: string };
  streamSettings?: { network?: string; security?: string };
}

function toFacts(values: FactsInput): CapabilityFacts {
  return {
    protocol: values.protocol,
    stream: {
      network: values.streamSettings?.network,
      security: values.streamSettings?.security,
    },
    settings: {
      encryption: values.settings?.encryption,
      decryption: values.settings?.decryption,
      method: values.settings?.method,
    },
  };
}

export function canEnableTls(values: CapabilityProtocolSlice): boolean {
  return can('tls', toFacts(values));
}

export function canEnableReality(values: CapabilityProtocolSlice): boolean {
  return can('reality', toFacts(values));
}

export function canEnableTlsFlow(values: CapabilityProtocolSlice): boolean {
  return can('tlsFlow', toFacts(values));
}

export function canEnableStream(values: { protocol: string }): boolean {
  return can('stream', { protocol: values.protocol });
}

export function canEnableSniffing(values: { protocol: string }): boolean {
  return can('sniffing', { protocol: values.protocol });
}

export function isSS2022(values: CapabilityShadowsocksSlice): boolean {
  return can('ss2022', toFacts(values));
}

/*
Not table-driven: it reads the clients array, and the rule grammar addresses only
scalars on purpose so a rule can never come to depend on config shape it should
not see. Vision seed needs Vision available AND a client actually using it.
*/
export function canEnableVisionSeed(values: CapabilityVlessSlice): boolean {
  if (!canEnableTlsFlow(values)) return false;
  const clients = values.settings?.clients;
  if (!Array.isArray(clients)) return false;
  return clients.some((c) => c?.flow === VISION_FLOW);
}

/*
Kept as code for parity with the legacy class: it returns true on non-SS
protocols too, because the method getter resolved to "" there. Callers all narrow
on protocol === shadowsocks first.
*/
export function isSSMultiUser(values: CapabilityShadowsocksSlice): boolean {
  const method = values.protocol === 'shadowsocks' ? (values.settings?.method ?? '') : '';
  return method !== SS_BLAKE3_CHACHA20;
}
