import { describe, expect, it } from 'vitest';

import { PROTOCOL_VALUES } from '@/generated/zod';
import { KIND_GROUPS, KIND_NAME_KEYS, kindLabel } from '@/lib/cores/kind-labels';

/*
The picker is built from KIND_GROUPS, so a kind no group lists cannot be
created from the form at all. KIND_NAME_KEYS is a Record over Protocol and
already fails to compile for a missing kind; the groups are a list, and only
this test holds them to the same rule.
*/
describe('kind labels', () => {
  it('lists every kind in exactly one group', () => {
    const seen = new Map<string, number>();
    for (const group of KIND_GROUPS) {
      for (const kind of group.kinds) seen.set(kind, (seen.get(kind) ?? 0) + 1);
    }
    for (const kind of PROTOCOL_VALUES) {
      expect(seen.get(kind), `${kind} is in ${seen.get(kind) ?? 0} groups`).toBe(1);
    }
    expect([...seen.keys()].sort()).toEqual([...PROTOCOL_VALUES].sort());
  });

  it('names every kind through its own key and leaves an unknown kind as it is', () => {
    const t = (key: string) => `<${key}>`;
    for (const kind of PROTOCOL_VALUES) {
      expect(kindLabel(t, kind)).toBe(`<${KIND_NAME_KEYS[kind]}>`);
    }
    expect(kindLabel(t, 'ftp')).toBe('ftp');
  });
});
