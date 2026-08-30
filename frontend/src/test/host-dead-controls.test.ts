import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/*
Two switches on the host form did nothing at all.

"Mihomo X25519" and "Shuffle host" are stored on the host row, round-tripped
through the entity and emitted in the OpenAPI examples — and read by no renderer
anywhere. Their neighbour on the same tab, Mihomo IP version, IS read: the Clash
renderer turns it into `ip-version`. These two have no such consumer in the
panel, in the frontend link builders, or in the docs site's own third renderer.

So an operator toggled them, saved, saw the value persist, and nothing about
their subscription changed. A control that does nothing is worse than a missing
one: it looks like a setting that did not work.

The columns and the API fields stay, and the form still carries their stored
values through a save — removing a switch must not silently clear what an
operator set with it. Whether to implement them or drop the columns is a
product question, recorded in DISCOVERIES-LEDGER.md L29. F141.
*/
describe('the host form', () => {
  const source = readFileSync(
    resolve(__dirname, '../pages/hosts/HostFormModal.tsx'),
    'utf8',
  );

  it.each(['mihomoX25519', 'shuffleHost'])(
    'offers no control for %s, which nothing reads',
    (field) => {
      expect(
        source.includes(`<FormField name="${field}"`),
        `the form offers a control for ${field}; no renderer reads it, so toggling it changes nothing about the subscription`,
      ).toBe(false);
    },
  );

  it.each(['mihomoX25519', 'shuffleHost'])(
    'still carries a stored %s through a save',
    (field) => {
      expect(
        source.includes(`${field}: host?.${field} ??`),
        `${field} is no longer seeded from the stored host, so saving any other change silently clears what an operator set before the control was removed`,
      ).toBe(true);
    },
  );

  // And the one on the same tab that IS read stays, or this looks like a sweep
  // rather than a removal of exactly what does nothing.
  it('keeps the Mihomo IP version control, which the Clash renderer reads', () => {
    expect(source).toContain('<FormField name="mihomoIpVersion"');
  });
});
