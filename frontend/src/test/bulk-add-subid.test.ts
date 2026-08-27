import { describe, it, expect } from 'vitest';

import { bulkClientSubId } from '@/lib/clients/bulk-subid';

/*
A typed subscription id is only meaningful for a single client.

The bulk-add modal built each payload with

    subId: current.subId || RandomUtil.randomLowerAndNum(16)

inside the per-email map, so with the field left alone every client got a fresh
id and all N were created — the default path was never broken, which is why this
survived. Type an id, or press the regenerate button beside it, and every
payload carried that one value.

A subId identifies one subscription and the panel refuses a duplicate everywhere
else (Create at client_crud.go:135, Update at :403). So the batch let the first
email claim it and skipped the rest: an operator asking for 50 clients got 1.
The modal did report the failures, so it was never silent — but the control gave
no hint that it could only ever be correct for a single client.

F173.
*/
describe('bulkClientSubId', () => {
  it('gives every client of a batch its own id, even when one is typed', () => {
    const ids = new Set(Array.from({ length: 50 }, () => bulkClientSubId('fixedsub123', 50)));
    expect(ids.size).toBe(50);
    expect(ids.has('fixedsub123')).toBe(false);
  });

  it('honours a typed id when exactly one client is being created', () => {
    expect(bulkClientSubId('fixedsub123', 1)).toBe('fixedsub123');
  });

  it('generates one when the field is left alone', () => {
    expect(bulkClientSubId('', 1)).toHaveLength(16);
    expect(bulkClientSubId('', 1)).not.toBe(bulkClientSubId('', 1));
  });

  it('generates for a batch whose field is left alone, which always worked', () => {
    const ids = new Set(Array.from({ length: 20 }, () => bulkClientSubId('', 20)));
    expect(ids.size).toBe(20);
  });
});
