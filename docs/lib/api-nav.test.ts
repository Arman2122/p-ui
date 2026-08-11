import { readdirSync, readFileSync } from 'node:fs';
import { describe, it, expect } from 'vitest';

// `pnpm gen:api` writes one MDX page per OpenAPI tag but never touches
// meta.json, so a new tag ships unreachable by browsing unless this fails.
const API_DIR = 'content/docs/en/reference/api';

function pagesOf(locale: string): string[] {
  const meta = readFileSync(`content/docs/${locale}/reference/api/meta.json`, 'utf8');
  return (JSON.parse(meta) as { pages: string[] }).pages;
}

describe('API reference navigation', () => {
  it('lists every generated tag page in the English sidebar', () => {
    const generated = readdirSync(API_DIR)
      .filter((name) => name.endsWith('.mdx'))
      .map((name) => name.replace(/\.mdx$/, ''))
      .filter((slug) => slug !== 'index')
      .sort();
    const listed = pagesOf('en')
      .filter((slug) => slug !== 'index')
      .sort();

    expect(listed).toEqual(generated);
  });

  // A locale with no copy of a page falls back to English, so the two navs
  // stay identical: a tag listed only in one is unreachable in the other.
  it('offers the same pages in Persian', () => {
    expect(pagesOf('fa')).toEqual(pagesOf('en'));
  });
});
