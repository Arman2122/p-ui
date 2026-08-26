import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { inboundFromDb } from '@/lib/xray/inbound-from-db';
import { genLink, genWireguardLinks, getInboundClients } from '@/lib/xray/inbound-link';

/*
The frontend half of the share-link golden corpus.

Share links are built more than once in this repo and nothing enforced that the
copies agree, which is the single cause behind fourteen of the 2026-08-20
audit's findings — always the same sentence with different nouns: renderer A
emits a field renderer B omits, so the panel shows one tunnel and the
subscription hands out another. The corpus states the parameters once; each
renderer has a harness that must reproduce them.

The Go half is internal/sub/golden_corpus_test.go and reads the same directory.
A fixture added for one renderer is a fixture the other is now held to, which is
the entire point — a fourth renderer is caught on its first commit rather than
on a support ticket.
*/

const corpusDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../testdata/sharelink',
);

interface Fixture {
  name: string;
  why: string;
  email: string;
  inbound: {
    protocol: string;
    listen: string;
    port: number;
    remark: string;
    settings: string;
    streamSettings: string;
    sniffing: string;
  };
  expected: {
    scheme: string;
    userinfo: string;
    host: string;
    port: number;
    params: Record<string, string>;
  };
  presentOnly: string[];
}

function loadCorpus(): Fixture[] {
  const files = readdirSync(corpusDir).filter((f) => f.endsWith('.json'));
  return files.map((file) => {
    const fixture = JSON.parse(readFileSync(path.join(corpusDir, file), 'utf8')) as Fixture;
    if (fixture.name !== file.replace(/\.json$/, '')) {
      throw new Error(`${file} declares name "${fixture.name}"; the two must match so a failure names the file`);
    }
    return fixture;
  });
}

const WIREGUARD_FAMILY = new Set(['wireguard', 'wgkernel', 'awgkernel']);

function render(fixture: Fixture): string {
  const inbound = inboundFromDb(fixture.inbound);

  if (WIREGUARD_FAMILY.has(fixture.inbound.protocol)) {
    /* The WireGuard builders fan out per peer rather than resolving one client
       by email, so take the peer the fixture names. */
    const links = genWireguardLinks({
      inbound,
      remark: fixture.inbound.remark,
      hostOverride: fixture.inbound.listen,
      fallbackHostname: fixture.inbound.listen,
    });
    return links.split(/\r?\n/)[0] ?? '';
  }

  const clients = getInboundClients(inbound) ?? [];
  const client = clients.find((c) => c.email === fixture.email);
  if (!client) {
    throw new Error(`fixture ${fixture.name} names client "${fixture.email}", which the inbound does not carry`);
  }
  return genLink({
    inbound,
    address: fixture.inbound.listen,
    port: fixture.inbound.port,
    remark: fixture.inbound.remark,
    client,
  }).split(/\r?\n/)[0] ?? '';
}

const corpus = loadCorpus();

describe('every corpus fixture renders its declared link', () => {
  it('has fixtures at all', () => {
    /* The corpus IS the test, so an empty directory is a failure rather than a
       vacuous pass. */
    expect(corpus.length).toBeGreaterThan(0);
  });

  corpus.forEach((fixture) => {
    describe(fixture.name, () => {
      const raw = render(fixture);
      const url = new URL(raw);

      it('renders a link at all', () => {
        expect(raw, fixture.why).not.toBe('');
      });

      it('uses the declared scheme, credential and endpoint', () => {
        expect(url.protocol).toBe(`${fixture.expected.scheme}:`);
        expect(decodeURIComponent(url.username)).toBe(fixture.expected.userinfo);
        expect(url.hostname).toBe(fixture.expected.host);
        expect(url.port).toBe(String(fixture.expected.port));
      });

      Object.entries(fixture.expected.params).forEach(([key, want]) => {
        it(`emits ${key}`, () => {
          expect(url.searchParams.get(key), `${key} — ${fixture.why}`).toBe(want);
        });
      });

      fixture.presentOnly.forEach((key) => {
        it(`emits ${key} at some value`, () => {
          expect(url.searchParams.has(key)).toBe(true);
        });
      });
    });
  });
});
