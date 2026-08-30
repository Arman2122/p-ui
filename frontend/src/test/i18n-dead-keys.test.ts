import { readFileSync, readdirSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/*
 * Guards the translation set two ways: every key in en-US must be
 * referenced somewhere in the frontend or Go sources (dead keys accumulate
 * silently — this test deleted over two hundred of them when it was
 * introduced), and every locale must carry exactly the en-US key set
 * (missing keys fall back to en-US at runtime, so nothing else fails the
 * build when a translation is forgotten).
 *
 * References are matched as whole dotted tokens, not substrings, so a dead
 * key cannot hide behind a longer live sibling. Dynamically built keys are
 * covered by harvesting the string-literal prefixes that appear next to
 * concatenation or template-literal interpolation; the prefix needs at
 * least one dot but need not end on one, so a literal that stops mid-leaf
 * right before the interpolation still keeps its subtree alive. This file
 * excludes itself from the scan so the prose above cannot whitelist
 * anything.
 */

const repoRoot = resolve(process.cwd(), '..');
const translationDir = join(repoRoot, 'internal', 'web', 'translation');
const selfPath = fileURLToPath(import.meta.url);

function flattenKeys(obj: Record<string, unknown>, prefix = ''): string[] {
  const keys: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const key = `${prefix}${k}`;
    if (v !== null && typeof v === 'object') {
      keys.push(...flattenKeys(v as Record<string, unknown>, `${key}.`));
    } else {
      keys.push(key);
    }
  }
  return keys;
}

function collectSources(dir: string, exts: string[], out: string[]): void {
  for (const [, body] of namedSourcesIn(dir, exts)) out.push(body);
}

// The same walk, keeping the path: a missing key is worth nothing to the reader
// without the file that asks for it.
function namedSourcesIn(dir: string, exts: string[], out: [string, string][] = []): [string, string][] {
  for (const entry of readdirSync(dir)) {
    if (['node_modules', 'dist', 'generated', '.git', '.local', 'storybook-static'].includes(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      namedSourcesIn(full, exts, out);
    } else if (exts.some((ext) => entry.endsWith(ext)) && resolve(full) !== selfPath) {
      out.push([full, readFileSync(full, 'utf8')]);
    }
  }
  return out;
}

describe('i18n keys', () => {
  const enUS = JSON.parse(readFileSync(join(translationDir, 'en-US.json'), 'utf8'));
  const enKeys = flattenKeys(enUS);

  /*
   * A Go constant that IS the key is not a use of it. This scan tokenises source
   * text, so `KeyNoSnat = "pages.xray.routing.refuse.noSnat"` made the key look
   * referenced by its own declaration - which is how selfLoop survived from
   * e30b1841 with no emit site anywhere. Ledger L7.
   */
  const GO_KEY_DECLARATION = /^[ \t]*(?:const |var )?[A-Za-z_][A-Za-z0-9_]*[ \t]*=[ \t]*"[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+"[ \t]*$/gm;

  const namedSources = [
    ...namedSourcesIn(join(repoRoot, 'frontend', 'src'), ['.ts', '.tsx']),
    ...namedSourcesIn(join(repoRoot, 'internal'), ['.go']),
  ];
  const tsSources: string[] = [];
  collectSources(join(repoRoot, 'frontend', 'src'), ['.ts', '.tsx'], tsSources);
  const goSources: string[] = [];
  collectSources(join(repoRoot, 'internal'), ['.go'], goSources);
  /*
   * A declaration is a use only if the constant it declares is used somewhere.
   * The keys are emitted through their Go identifier, never through the dotted
   * string, so stripping declarations outright would condemn every live one.
   */
  const declaredKeys = new Map<string, string>();
  const sources = [
    ...tsSources,
    ...goSources.map((src) => src.replace(GO_KEY_DECLARATION, (line) => {
      const parts = line.match(/([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]+)"/);
      if (parts) declaredKeys.set(parts[1], parts[2]);
      return '';
    })),
  ];
  const blob = sources.join('\n');

  const tokens = new Set(blob.match(/[A-Za-z][A-Za-z0-9_.]*/g) ?? []);

  for (const [identifier, key] of declaredKeys) {
    if (tokens.has(identifier)) tokens.add(key);
  }

  const prefixes: string[] = [];
  for (const match of blob.matchAll(/['"`]([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+\.?)['"`]\s*\+/g)) {
    prefixes.push(match[1]);
  }
  for (const match of blob.matchAll(/[`']([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+\.?)\$\{/g)) {
    prefixes.push(match[1]);
  }

  it('every en-US key is referenced by the frontend or Go sources', () => {
    const dead = enKeys.filter(
      (key) => !tokens.has(key) && !prefixes.some((p) => key.startsWith(p)),
    );
    expect(dead, `dead i18n keys (delete from all locales):\n  ${dead.join('\n  ')}`).toEqual([]);
  });

  /*
   * The direction the other two never covered: a t('a.b.c') whose key is in no
   * locale at all shipped green, because both existing checks start FROM the
   * key set. i18next then renders the raw dotted key, and the operator reads
   * "pages.xray.balancer" where a word should be. Two such keys were live when
   * this was added; one of them was hiding behind `|| 'Balancer'`, which never
   * fires because the miss is a truthy string.
   *
   * A positional default (`t('a.b.c', 'Some text')`) does not excuse it: the
   * English reads fine and every other locale still shows English. F197 / F198.
   */
  it('every key the sources ask for exists in en-US', () => {
    const asked = new Map<string, string>();
    for (const [file, src] of namedSources) {
      for (const match of src.matchAll(/\bt\(\s*(['"`])([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+)\1/g)) {
        if (!asked.has(match[2])) asked.set(match[2], file);
      }
      for (const match of src.matchAll(/I18nWeb\(c,\s*"([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+)"/g)) {
        if (!asked.has(match[1])) asked.set(match[1], file);
      }
    }
    const enSet = new Set(enKeys);
    const absent = [...asked].filter(([key]) => !enSet.has(key));
    expect(
      absent.map(([key, file]) => `${key} (${file})`),
      'i18n keys the UI asks for that no locale defines; each renders as the raw key:',
    ).toEqual([]);
  });

  it('every locale carries exactly the en-US key set', () => {
    const enSet = new Set(enKeys);
    for (const file of readdirSync(translationDir)) {
      if (!file.endsWith('.json') || file === 'en-US.json') continue;
      const keys = new Set(flattenKeys(JSON.parse(readFileSync(join(translationDir, file), 'utf8'))));
      const missing = enKeys.filter((k) => !keys.has(k));
      const orphans = [...keys].filter((k) => !enSet.has(k));
      expect(missing, `${file} is missing keys:\n  ${missing.join('\n  ')}`).toEqual([]);
      expect(orphans, `${file} has keys absent from en-US:\n  ${orphans.join('\n  ')}`).toEqual([]);
    }
  });
});
