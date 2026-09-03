// Client-side mirror of the backend inbound-tag derivation
import { isWireguardLike } from '@/lib/xray/kernel-wireguard';
// (web/service/port_conflict.go). Keep in sync; inbound-tag.test.ts guards parity.

type TransportBits = number;
const TCP: TransportBits = 1;
const UDP: TransportBits = 2;
/* Only a kind that declares it binds nothing reaches zero bits. */
const NONE: TransportBits = 0;

function asString(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/*
What each core answers for its own kinds, which is what the Go side asks the
registry for rather than deriving. Only the kinds Xray serves read the stream
settings below, so every other kind is answered here or its tag is wrong.
A protocol missing from this switch falls through to Xray's rules, and
inbound-tag.test.ts fails on any protocol nobody covers.
*/
function coreTransports(
  protocol: string,
  settings: Record<string, unknown> | undefined,
): TransportBits | undefined {
  switch (protocol) {
    case 'hysteria':
      return UDP;
    case 'tun':
      /* A kernel interface binds no port: the stored one is a form field. */
      return NONE;
    case 'tuic':
      return UDP;
    case 'anytls':
    case 'ssh':
    case 'mtproto':
      return TCP;
    case 'ocserv':
      /* One port carries both: TLS for the session, DTLS for the data channel. */
      return TCP | UDP;
    case 'openvpn':
      return asString(settings?.proto) === 'tcp' ? TCP : UDP;
    default:
      return isWireguardLike(protocol) ? UDP : undefined;
  }
}

function inboundTransports(
  protocol: string,
  streamSettings: Record<string, unknown> | undefined,
  settings: Record<string, unknown> | undefined,
): TransportBits {
  const declared = coreTransports(protocol, settings);
  if (declared !== undefined) return declared;

  let bits: TransportBits = 0;
  const network = asString(streamSettings?.network);
  if (network === 'kcp' || network === 'quic') bits |= UDP;
  else bits |= TCP;

  if (settings) {
    if (protocol === 'shadowsocks' || protocol === 'tunnel') {
      const key = protocol === 'tunnel' ? 'allowedNetwork' : 'network';
      const n = asString(settings[key]);
      if (n !== '') {
        bits = 0;
        for (const part of n.split(',')) {
          const p = part.trim();
          if (p === 'tcp') bits |= TCP;
          else if (p === 'udp') bits |= UDP;
        }
      }
    } else if (protocol === 'mixed') {
      if (settings.udp === true) bits |= UDP;
    }
  }

  if (bits === 0) bits = TCP;
  return bits;
}

function transportTagSuffix(bits: TransportBits): string {
  if (bits === NONE) return 'none';
  if (bits === TCP) return 'tcp';
  if (bits === UDP) return 'udp';
  if (bits === (TCP | UDP)) return 'tcpudp';
  return 'any';
}

function baseInboundTag(port: number): string {
  return `in-${port}`;
}

function nodeTagPrefix(nodeId: number | null | undefined): string {
  return nodeId == null ? '' : `n${nodeId}-`;
}

export interface InboundTagInput {
  port: number;
  nodeId: number | null | undefined;
  protocol: string;
  streamSettings?: Record<string, unknown>;
  settings?: Record<string, unknown>;
}

export function composeInboundTag(input: InboundTagInput): string {
  const bits = inboundTransports(input.protocol, input.streamSettings, input.settings);
  return (
    nodeTagPrefix(input.nodeId)
    + baseInboundTag(input.port ?? 0)
    + '-'
    + transportTagSuffix(bits)
  );
}

export function isAutoInboundTag(tag: string, input: InboundTagInput): boolean {
  if (tag === '') return true;
  const base = composeInboundTag(input);
  if (tag === base) return true;
  const prefix = `${base}-`;
  if (!tag.startsWith(prefix)) return false;
  const suffix = tag.slice(prefix.length);
  return suffix !== '' && /^[0-9]+$/.test(suffix);
}
