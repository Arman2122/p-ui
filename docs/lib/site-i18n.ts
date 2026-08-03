import type { Locale } from './i18n';

// UI strings for the marketing chrome (landing page hero/features/footer + the
// shared navbar labels). The docs *pages* are translated as MDX under
// content/docs/{locale}; this covers the React-rendered home page and nav that
// can't live in MDX. English is the source; fa falls back to en.
//
// Convention matches the docs: translate prose only — product/protocol names
// (p-ui, Xray, VLESS, REALITY, x25519, Docker, REST API, …) stay in Latin.
export interface SiteMessages {
  tagline: string;
  getStarted: string;
  viewOnGitHub: string;
  documentation: string;
  donate: string;
  docs: string;
  stars: string;
  forks: string;
  latest: string;
  copyCommand: string;
  copied: string;
  featuresHeading: string;
  featuresSubtitle: string;
  // Order matches the icon list in components/home/features.tsx.
  features: { title: string; description: string }[];
  // Footer license line: `{app} — {before}<a>GPL-3.0</a>{after}` (spacing baked in).
  licenseBefore: string;
  licenseAfter: string;
}

const en: SiteMessages = {
  tagline: 'Advanced web panel for managing Xray-core servers',
  getStarted: 'Get started',
  viewOnGitHub: 'View on GitHub',
  documentation: 'Documentation',
  donate: 'Donate',
  docs: 'Docs',
  stars: 'stars',
  forks: 'forks',
  latest: 'latest',
  copyCommand: 'Copy install command',
  copied: 'Copied',
  featuresHeading: 'Everything you need to run Xray',
  featuresSubtitle:
    'A modern, fast control panel for Xray-core — built for operators who want power without the command-line grind.',
  features: [
    {
      title: 'Every major protocol',
      description:
        'VLESS, VMess, Trojan, Shadowsocks, WireGuard, Hysteria2, SOCKS, HTTP and Dokodemo-door — managed from one panel.',
    },
    {
      title: 'REALITY & XTLS-Vision',
      description:
        'First-class support for VLESS + REALITY with x25519 keys, short IDs and the xtls-rprx-vision flow for stealth and speed.',
    },
    {
      title: 'Clients & traffic control',
      description:
        'Per-client traffic quotas, expiry dates, IP limits and live online status, with one-click share links and QR codes.',
    },
    {
      title: 'Multi-node & subscriptions',
      description:
        'Coordinate multiple servers, managed hosts and external proxies, and serve VLESS / Clash / JSON subscriptions.',
    },
    {
      title: 'Telegram bot & alerts',
      description:
        'Built-in Telegram notifications for traffic caps, expiry warnings and system load, plus admin actions.',
    },
    {
      title: 'Self-hosted & scriptable',
      description:
        'A single Go binary or Docker image, an SQLite/PostgreSQL backend, and a full REST API for automation.',
    },
  ],
  licenseBefore: 'released under the ',
  licenseAfter: ' license.',
};

const fa: SiteMessages = {
  tagline: 'پنل وب پیشرفته برای مدیریت سرورهای Xray-core',
  getStarted: 'شروع کنید',
  viewOnGitHub: 'مشاهده در GitHub',
  documentation: 'مستندات',
  donate: 'حمایت مالی',
  docs: 'مستندات',
  stars: 'ستاره',
  forks: 'فورک',
  latest: 'آخرین',
  copyCommand: 'کپی دستور نصب',
  copied: 'کپی شد',
  featuresHeading: 'هر آنچه برای اجرای Xray لازم دارید',
  featuresSubtitle:
    'یک پنل کنترلِ مدرن و سریع برای Xray-core — ساخته‌شده برای ادمین‌هایی که قدرت می‌خواهند، بدون درگیری با خط فرمان.',
  features: [
    {
      title: 'همه‌ی پروتکل‌های اصلی',
      description:
        'VLESS، VMess، Trojan، Shadowsocks، WireGuard، Hysteria2، SOCKS، HTTP و Dokodemo-door — همه از یک پنل مدیریت می‌شوند.',
    },
    {
      title: 'REALITY و XTLS-Vision',
      description:
        'پشتیبانی درجه‌یک از VLESS + REALITY با کلیدهای x25519، short ID‌ها و فلوی xtls-rprx-vision برای مخفی‌کاری و سرعت.',
    },
    {
      title: 'کلاینت‌ها و کنترل ترافیک',
      description:
        'سهمیه‌ی ترافیک برای هر کلاینت، تاریخ انقضا، محدودیت IP و وضعیت آنلاینِ زنده، همراه با لینک‌های اشتراک‌گذاری و کدهای QR تنها با یک کلیک.',
    },
    {
      title: 'چندنودی و سابسکریپشن‌ها',
      description:
        'هماهنگ‌سازی چند سرور، هاست‌های مدیریت‌شده و پروکسی‌های خارجی، و ارائه‌ی سابسکریپشن‌های VLESS / Clash / JSON.',
    },
    {
      title: 'ربات Telegram و هشدارها',
      description:
        'اعلان‌های داخلیِ Telegram برای سقف ترافیک، هشدار انقضا و بار سیستم، به‌علاوه‌ی کنش‌های مدیریتی.',
    },
    {
      title: 'خودمیزبان و قابل‌اسکریپت',
      description:
        'یک باینری Go یا ایمیج Docker، بک‌اندِ SQLite/PostgreSQL، و یک REST API کامل برای خودکارسازی.',
    },
  ],
  licenseBefore: 'تحت مجوز ',
  licenseAfter: ' منتشر شده است.',
};

const messages: Record<Locale, SiteMessages> = { en, fa };

export function getSiteMessages(lang: string): SiteMessages {
  return messages[lang as Locale] ?? en;
}
