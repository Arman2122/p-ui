import type { CSSProperties, ReactNode } from 'react';

/* The panel's own 24x24 stroke set, imported from the Penhoon Panel design.
   antd's icon package stays in use where a control already ships one. */
const P = (d: string) => <path d={d} />;

const ICONS = {
  gauge: <><path d="M12 14a2 2 0 1 0 0-4 2 2 0 0 0 0 4z" /><path d="M13.4 12.6 17 9M3.5 18a9.5 9.5 0 1 1 17 0" /></>,
  inbounds: <><path d="M4 4h9M4 4v9" /><path d="M20 14v6H4v-6M14 10l6-6M20 4h-5M20 4v5" /></>,
  users: <><circle cx="10" cy="8.4" r="3.4" /><path d="M3.6 20a6.4 6.4 0 0 1 12.8 0" /><path d="M16.4 5.4a3.4 3.4 0 0 1 0 6M18 20a6.5 6.5 0 0 0-1.6-4.3" /></>,
  groups: <><path d="M20.6 13.4 13.4 20.6a2 2 0 0 1-2.8 0l-7.2-7.2a2 2 0 0 1-.4-2.3l3-6A2 2 0 0 1 7.8 4h6.4a2 2 0 0 1 1.4.6l5 5a2 2 0 0 1 0 2.8z" /><circle cx="9" cy="9" r="1.4" /></>,
  speed: <>{P('M13 2 4.5 13.5H11l-1 8.5 8.5-11.5H12z')}</>,
  nodes: <><rect x="3" y="4" width="18" height="7" rx="2" /><rect x="3" y="14" width="18" height="6" rx="2" /><path d="M7 7.5h.01M7 17h.01" /></>,
  hosts: <><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c2.5 2.6 2.5 15.4 0 18M12 3c-2.5 2.6-2.5 15.4 0 18" /></>,
  outbounds: <><path d="M14 4h6v6" /><path d="M20 4 11 13" /><path d="M18 14v6H4V6h6" /></>,
  routing: <><path d="M4 7h6l4 10h6" /><path d="M17 4l3 3-3 3M17 14l3 3-3 3" /></>,
  settings: <><circle cx="12" cy="12" r="6.4" /><circle cx="12" cy="12" r="2.6" /><path d="M19.5 12h2.5M12 4.5V2M4.5 12H2M12 19.5V22M17.3 6.7l1.77-1.77M6.7 6.7 4.93 4.93M6.7 17.3l-1.77 1.77M17.3 17.3l1.77 1.77" /></>,
  xray: <>{P('M4 14.5 9.5 9m0 0L4 3.5M9.5 9l4 4.5m0 0L20 20.5m-6.5-7L20 6.5')}</>,
  api: <><circle cx="7.5" cy="16.5" r="3.5" /><path d="m10 14 8-8M15 4h5v5" /></>,
  logout: <><path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3" /><path d="M10 8l-4 4 4 4M6 12h9" /></>,
  github: <>{P('M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.7c-2.78.6-3.37-1.34-3.37-1.34-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.89 1.53 2.34 1.09 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.56-1.11-4.56-4.95 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.65 0 0 .84-.27 2.75 1.02a9.4 9.4 0 0 1 5 0c1.91-1.29 2.75-1.02 2.75-1.02.55 1.38.2 2.4.1 2.65.64.7 1.03 1.59 1.03 2.68 0 3.85-2.34 4.7-4.57 4.95.36.31.68.92.68 1.85v2.74c0 .27.18.58.69.48A10 10 0 0 0 12 2z')}</>,
  bell: <><path d="M18 9a6 6 0 1 0-12 0c0 5-2 6-2 6h16s-2-1-2-6" /><path d="M10.5 20a2 2 0 0 0 3 0" /></>,
  moon: <>{P('M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z')}</>,
  sun: <><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M19 5l-1.5 1.5M6.5 17.5 5 19" /></>,
  search: <><circle cx="11" cy="11" r="7" /><path d="m20 20-3.8-3.8" /></>,
  plus: <>{P('M12 5v14M5 12h14')}</>,
  filter: <>{P('M3 5h18l-7 8v6l-4-2v-4z')}</>,
  sort: <>{P('M7 4v16m0 0-3-3m3 3 3-3M17 20V4m0 0-3 3m3-3 3 3')}</>,
  qr: <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><path d="M14 14h3v3h-3zM19 19h2v2h-2z" /></>,
  copy: <><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h8" /></>,
  chart: <>{P('M4 20V10M10 20V4M16 20v-7M22 20H2')}</>,
  edit: <>{P('M4 20h4L20 8l-4-4L4 16z')}</>,
  more: <><circle cx="12" cy="5" r="1.2" /><circle cx="12" cy="12" r="1.2" /><circle cx="12" cy="19" r="1.2" /></>,
  trash: <><path d="M4 7h16M10 7V4h4v3M6 7l1 13h10l1-13" /></>,
  power: <><path d="M12 3v9" /><path d="M18.4 6.6a9 9 0 1 1-12.8 0" /></>,
  restart: <><path d="M20 12a8 8 0 1 1-2.3-5.7" /><path d="M20 4v4h-4" /></>,
  logs: <>{P('M4 6h16M4 12h16M4 18h10')}</>,
  config: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M8 9h8M8 13h5" /></>,
  backup: <><path d="M12 3v10m0 0 3.5-3.5M12 13 8.5 9.5" /><path d="M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3" /></>,
  history: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l4 2" /></>,
  up: <>{P('M12 19V5m0 0-6 6m6-6 6 6')}</>,
  down: <>{P('M12 5v14m0 0 6-6m-6 6-6-6')}</>,
  chevron: <>{P('m9 6 6 6-6 6')}</>,
  chevronDown: <>{P('m6 9 6 6 6-6')}</>,
  panelLeft: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M9 4v16" /></>,
  check: <>{P('m5 13 4 4L19 7')}</>,
  close: <>{P('M6 6l12 12M18 6 6 18')}</>,
  eye: <><path d="M2 12s3.6-6 10-6 10 6 10 6-3.6 6-10 6-10-6-10-6z" /><circle cx="12" cy="12" r="2.5" /></>,
  eyeOff: <><path d="M4 4l16 16" /><path d="M9.5 5.5A9.6 9.6 0 0 1 12 5c6.4 0 10 6 10 6s-1.2 2-3.3 3.7M6.4 7.6C3.7 9.3 2 12 2 12s3.6 6 10 6a9.7 9.7 0 0 0 3-.5" /></>,
  user: <><circle cx="12" cy="8" r="3.5" /><path d="M5 20c0-3.5 3-6 7-6s7 2.5 7 6" /></>,
  lock: <><rect x="4" y="10" width="16" height="10" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3" /></>,
  shield: <>{P('M12 3l8 3v6c0 5-3.4 8.2-8 9-4.6-.8-8-4-8-9V6z')}</>,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 8v4l3 2" /></>,
  cpu: <><rect x="7" y="7" width="10" height="10" rx="2" /><path d="M4 10v4M20 10v4M10 4h4M10 20h4" /></>,
  ram: <><rect x="3" y="7" width="18" height="10" rx="2" /><path d="M7 17v3M12 17v3M17 17v3" /></>,
  swap: <>{P('M4 8h13m0 0-3-3m3 3-3 3M20 16H7m0 0 3-3m-3 3 3 3')}</>,
  disk: <><circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="2.5" /></>,
  link: <><path d="M9 15l6-6" /><path d="M11 6.5 13 4.5a4 4 0 0 1 6 6l-2 2M13 17.5l-2 2a4 4 0 0 1-6-6l2-2" /></>,
  mail: <><rect x="3" y="5" width="18" height="14" rx="2" /><path d="m3 7 9 6 9-6" /></>,
  calendar: <><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M3 10h18M8 3v4M16 3v4" /></>,
  bolt: <>{P('M13 2 5 13h5l-1 9 8-12h-5z')}</>,
  refresh: <><path d="M4 12a8 8 0 0 1 13.7-5.7L20 8" /><path d="M20 4v4h-4M20 12a8 8 0 0 1-13.7 5.7L4 16" /><path d="M4 20v-4h4" /></>,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof ICONS;

export const iconNames = Object.keys(ICONS) as IconName[];

export function Icon({ name, size = 16, style }: { name: IconName; size?: number; style?: CSSProperties }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      style={{ flex: '0 0 auto', ...style }}
    >
      {ICONS[name]}
    </svg>
  );
}

export default Icon;
