import { kindModule } from '@/lib/cores/kinds';

/**
 * Protocols that hold more than one client per inbound, so every client picker
 * filters on the same answer. A hand-kept list drifted twice (mtproto, then
 * ocserv); the kind module is the one place a core says so.
 */
export function supportsMultipleClients(protocol?: string): boolean {
  const rule = kindModule((protocol ?? '').toLowerCase())?.multiUser ?? false;
  return typeof rule === 'function' ? true : rule;
}
