import { docs } from 'collections/server';
import { loader } from 'fumadocs-core/source';
import { lucideIconsPlugin } from 'fumadocs-core/source/lucide-icons';
import { openapiPlugin } from 'fumadocs-openapi/server';
import { i18n } from '@/lib/i18n';
import { basePath, docsContentRoute, docsImageRoute, docsRoute } from './shared';

// See https://fumadocs.dev/docs/headless/source-api for more info.
// `i18n` builds one page tree per language; untranslated pages fall back to en.
// `openapiPlugin` adds HTTP-method badges to generated API reference pages.
export const source = loader({
  i18n,
  baseUrl: docsRoute,
  source: docs.toFumadocsSource(),
  plugins: [lucideIconsPlugin(), openapiPlugin()],
});

// `segments` feed generateStaticParams (route params never carry `basePath`);
// `url` is what gets rendered/fetched, so it may need the subpath prefix.

export function getPageImage(page: (typeof source)['$inferPage']) {
  const segments = [...page.slugs, 'image.png'];

  return {
    segments,
    // Consumed only as `openGraph.images`, which Next resolves against
    // `metadataBase` — and `metadataBase` is `siteUrl`, which already carries
    // the subpath. Adding `basePath` here would duplicate it.
    url: `${docsImageRoute}/${segments.join('/')}`,
  };
}

export function getPageMarkdownUrl(page: (typeof source)['$inferPage']) {
  const segments = [...page.slugs, 'content.md'];

  return {
    segments,
    // Consumed by fumadocs' MarkdownCopyButton / ViewOptionsPopover, which put
    // it straight into `fetch()` and a plain `<a href>`. Their `withBasePath`
    // helper only reads Vite's `import.meta.env.BASE_URL`, so under Next it is
    // a no-op — prefix it here or "Copy Markdown" / "View as Markdown" 404 on
    // the GitHub Pages project page.
    url: `${basePath}${docsContentRoute}/${segments.join('/')}`,
  };
}

export async function getLLMText(page: (typeof source)['$inferPage']) {
  const processed = await page.data.getText('processed');

  return `# ${page.data.title} (${page.url})

${processed}`;
}
