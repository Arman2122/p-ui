/**
 * Protocols that hold more than one client per inbound, so every client picker
 * filters on the same list. The copies this replaced had already drifted: the
 * bulk-add modal was missing mtproto and silently hid those inbounds.
 */
const MULTI_CLIENT_PROTOCOLS = new Set([
  'shadowsocks', 'vless', 'vmess', 'trojan', 'hysteria', 'wireguard', 'wgkernel', 'awgkernel', 'mtproto',
]);

export function supportsMultipleClients(protocol?: string): boolean {
  return MULTI_CLIENT_PROTOCOLS.has((protocol ?? '').toLowerCase());
}
