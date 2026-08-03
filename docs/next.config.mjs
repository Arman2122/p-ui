import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

// Set DEPLOY_TARGET=static to produce a fully static export (e.g. for GitHub
// Pages). Search already uses a static index, and OG images are prerendered, so
// the export is self-contained. Default (unset) builds for Vercel/Node hosting.
const isStaticExport = process.env.DEPLOY_TARGET === 'static';

// Where the site is published. Same variable and same default as `siteUrl` in
// lib/shared.ts — keep the two in sync.
const siteUrl = process.env.NEXT_PUBLIC_SITE_URL || 'https://arman2122.github.io/p-ui';

// Path the site is served from, taken straight out of `siteUrl` so it can never
// disagree with the canonical/OG URLs built from it. Trailing slashes are
// dropped: a GitHub Pages *project* page (the default,
// https://arman2122.github.io/p-ui) yields `/p-ui`, while a custom domain
// (NEXT_PUBLIC_SITE_URL=https://docs.example.com) yields '' — no prefix.
function sitePathPrefix(url) {
  try {
    return new URL(url).pathname.replace(/\/+$/, '');
  } catch {
    throw new Error(`NEXT_PUBLIC_SITE_URL must be an absolute URL, got: ${JSON.stringify(url)}`);
  }
}

// Only the static export is served from a subpath; Vercel/Node hosting and
// `next dev` always serve the domain root.
const basePath = isStaticExport ? sitePathPrefix(siteUrl) : '';

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  // `basePath` prefixes everything Next itself emits (/_next/* chunks, <Link>
  // hrefs, the router), and `assetPrefix` pins static assets to the same place.
  // Hand-written root-absolute URLs are NOT rewritten — raw <img src>, metadata
  // icons, fetch() targets — so publish the prefix for app code as well;
  // lib/shared.ts re-exports it as `basePath`.
  env: { NEXT_PUBLIC_BASE_PATH: basePath },
  ...(basePath ? { basePath, assetPrefix: basePath } : {}),
  // On the static host (GitHub Pages) emit directory-style routes
  // (`en/index.html` rather than `en.html`) so URLs with a trailing slash —
  // including the root `/` → `/en/` redirect — resolve instead of 404ing.
  ...(isStaticExport
    ? { output: 'export', trailingSlash: true, images: { unoptimized: true } }
    : {}),
};

export default withMDX(config);
