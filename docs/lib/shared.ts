// Display name for the product (page titles, hero, nav, footer, OG cards).
// The lowercase `p-ui` spelling is reserved for the binary / CLI / install paths.
export const appName = 'Penhoon UI';
export const appTagline = 'Advanced web panel for managing Xray-core servers';

export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';

// The Penhoon UI product repository — used for the navbar GitHub link,
// build-time star/release stats, and install commands.
export const productRepo = {
  user: 'Arman2122',
  repo: 'p-ui',
  branch: 'main',
};

// Where these docs live in the Penhoon UI monorepo — used for "Edit on GitHub" links.
export const gitConfig = {
  user: 'Arman2122',
  repo: 'p-ui',
  branch: 'main',
  docsDir: 'docs/content/docs',
};

export const productRepoUrl = `https://github.com/${productRepo.user}/${productRepo.repo}`;

// AI-generated interactive wiki of the Penhoon UI codebase.
export const deepWikiUrl = `https://deepwiki.com/${productRepo.user}/${productRepo.repo}`;

// Community channel on Telegram. Penhoon UI has no channel of its own yet, so
// this points at the upstream 3x-ui community channel — the same one the docs
// link and describe as upstream's (see content/docs/*/help/faq.mdx and
// help/contributing.mdx). Replace this handle once a Penhoon UI channel exists;
// the nav label in layout.shared.tsx should say "upstream 3x-ui" until then.
export const telegramChannel = 'XrayUI';
export const telegramChannelUrl = `https://t.me/${telegramChannel}`;

// Public site origin, used for metadataBase / canonical URLs / OG images.
// Defaults to the GitHub Pages URL of the product repo, so the env var is
// optional; set NEXT_PUBLIC_SITE_URL when publishing under a custom domain.
// Use `||` (not `??`) so an empty string — e.g. an unset
// `${{ vars.NEXT_PUBLIC_SITE_URL }}` in CI — also falls back instead of
// shipping a blank origin.
export const siteUrl =
  process.env.NEXT_PUBLIC_SITE_URL || `https://${productRepo.user.toLowerCase()}.github.io/${productRepo.repo}`;

// Path prefix the site is served under — `/p-ui` on the GitHub Pages project
// page, empty on a custom domain, on Vercel/Node and in `next dev`.
// next.config.mjs derives it from `siteUrl` and inlines it here at build time.
// Next already applies it to its own output (/_next/* chunks, <Link>, the
// router); use this constant for the URLs it does NOT rewrite — raw <img src>,
// metadata icons and fetch() targets. Absolute URLs should use `siteUrl`, which
// already carries the same path.
export const basePath = process.env.NEXT_PUBLIC_BASE_PATH ?? '';
