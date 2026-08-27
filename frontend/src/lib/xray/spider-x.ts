import { sha256 } from '@noble/hashes/sha2.js';
import { bytesToHex, utf8ToBytes } from '@noble/hashes/utils.js';

// Mirrors deriveSpiderX in internal/sub/service.go byte-for-byte so panel
// links and subscription links agree; returns '' when there is no seed and
// no client key (the caller then omits spx, as the legacy builder did).
export function deriveSpiderX(seed: string, clientKey: string): string {
  if (!seed && !clientKey) return '';
  return `/${bytesToHex(sha256(utf8ToBytes(`${seed}|${clientKey}`))).slice(0, 15)}`;
}

/*
Mirrors deriveListIndex in internal/sub/service.go: the top eight bytes of
sha256(`${field}|${clientKey}`) read big-endian, modulo the list length.

A REALITY link carries one sni and one sid while the inbound declares lists, so
something has to pick. The pick is per client and stable, not per render — see
ledger L17. `field` separates the two picks, which would otherwise always land
on the same index and only ever produce the diagonal pairs.
*/
export function deriveListIndex(field: string, clientKey: string, n: number): number {
  if (n <= 1) return 0;
  const digest = sha256(utf8ToBytes(`${field}|${clientKey}`));
  let value = 0n;
  for (let i = 0; i < 8; i += 1) value = (value << 8n) | BigInt(digest[i]);
  return Number(value % BigInt(n));
}
