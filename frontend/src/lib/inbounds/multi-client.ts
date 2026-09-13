import { kindModule } from '@/lib/cores/kinds';

// Every client picker filters on the kind module's answer; a hand-kept list
// drifted twice (mtproto, then ocserv).
export function supportsMultipleClients(protocol?: string): boolean {
  const rule = kindModule((protocol ?? '').toLowerCase())?.multiUser ?? false;
  return typeof rule === 'function' ? true : rule;
}
