import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import { appName, appTagline, basePath, siteUrl } from '@/lib/shared';

// Global SEO defaults. The real <html>/<body> live in `app/[lang]/layout.tsx`
// so we can set `lang`/`dir` per locale (RTL for fa); this root layout is a
// pass-through that only carries site-wide metadata.
export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: {
    default: `${appName} — ${appTagline}`,
    template: `%s — ${appName}`,
  },
  description: appTagline,
  applicationName: appName,
  openGraph: {
    siteName: appName,
    type: 'website',
  },
  twitter: {
    card: 'summary_large_image',
  },
  // Icon hrefs are emitted verbatim — unlike OG images they are not resolved
  // against `metadataBase`, and Next does not apply `basePath` to them — so the
  // subpath prefix has to be added here by hand.
  icons: {
    icon: `${basePath}/favicon.png`,
    apple: `${basePath}/icon.png`,
  },
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return children;
}
