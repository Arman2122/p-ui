import type { IconName } from '@/components/ui/Icon';

export type NavItem = {
  key: string;
  labelKey: string;
  icon: IconName;
  /** Which count, if any, the sidebar badge shows for this item. */
  count?: 'cores' | 'plans';
};

export type NavGroup = { groupKey: string | null; items: NavItem[] };

export const NAV: NavGroup[] = [
  { groupKey: null, items: [{ key: '/', labelKey: 'menu.dashboard', icon: 'gauge' }] },
  {
    groupKey: 'menu.groupManage',
    items: [
      { key: '/inbounds', labelKey: 'menu.inbounds', icon: 'inbounds' },
      { key: '/clients', labelKey: 'menu.clients', icon: 'users' },
      { key: '/plans', labelKey: 'menu.policies', icon: 'speed', count: 'plans' },
      { key: '/groups', labelKey: 'menu.groups', icon: 'groups' },
      { key: '/resellers', labelKey: 'resellers.title', icon: 'users' },
    ],
  },
  {
    groupKey: 'menu.groupNetwork',
    items: [
      { key: '/nodes', labelKey: 'menu.nodes', icon: 'nodes' },
      { key: '/hosts', labelKey: 'menu.hosts', icon: 'hosts' },
      { key: '/outbound', labelKey: 'menu.outbounds', icon: 'outbounds' },
      { key: '/routing', labelKey: 'menu.routing', icon: 'routing' },
    ],
  },
  {
    groupKey: 'menu.groupSystem',
    items: [
      { key: '/cores', labelKey: 'menu.cores', icon: 'xray', count: 'cores' },
      { key: '/xray', labelKey: 'menu.xray', icon: 'config' },
      { key: '/settings', labelKey: 'menu.settings', icon: 'settings' },
    ],
  },
  { groupKey: 'menu.groupIntegrate', items: [{ key: '/api-docs', labelKey: 'menu.apiDocs', icon: 'api' }] },
];

const SECTION_OF = new Map<string, string | null>();
for (const group of NAV) for (const item of group.items) SECTION_OF.set(item.key, group.groupKey);

export function sectionKeyOf(route: string): string | null {
  return SECTION_OF.get(route === '/policies' ? '/plans' : route) ?? null;
}

export function navItemOf(route: string): NavItem | undefined {
  return NAV.flatMap((g) => g.items).find((i) => i.key === (route === '/policies' ? '/plans' : route));
}
