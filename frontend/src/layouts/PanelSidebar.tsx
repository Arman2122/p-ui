import { Fragment } from 'react';
import { useTranslation } from 'react-i18next';
import { Tooltip } from 'antd';

import { Icon } from '@/components/ui/Icon';
import { NAV, type NavItem } from '@/layouts/nav';
import { formatPanelVersion } from '@/lib/panel-version';

const REPO_URL = 'https://github.com/Arman2122/p-ui';

function NavButton({ item, active, collapsed, count, onClick }: {
  item: NavItem;
  active: boolean;
  collapsed: boolean;
  count?: number;
  onClick: () => void;
}) {
  const { t } = useTranslation();
  const label = t(item.labelKey);
  const body = (
    <button type="button" className={`p-navitem${active ? ' is-active' : ''}`} onClick={onClick} aria-current={active ? 'page' : undefined}>
      <span className="p-navicon"><Icon name={item.icon} size={16} /></span>
      {!collapsed && (
        <>
          <span className="p-navitem-label">{label}</span>
          {count != null && <span className="p-navbadge">{count}</span>}
        </>
      )}
    </button>
  );
  return collapsed ? <Tooltip title={label} placement="right">{body}</Tooltip> : body;
}

export default function PanelSidebar({ route, collapsed, open, counts, onNavigate }: {
  route: string;
  collapsed: boolean;
  open: boolean;
  counts: Partial<Record<NonNullable<NavItem['count']>, number>>;
  onNavigate: (key: string) => void;
}) {
  const { t } = useTranslation();
  const version = formatPanelVersion(window.PUI_CUR_VER || '');
  return (
    <aside className={`p-rail${collapsed ? ' is-collapsed' : ''}${open ? ' is-open' : ''}`}>
      <div className="p-brand">
        <span className="p-brand-mark">P</span>
        {!collapsed && <span className="p-brand-txt">Penhoon</span>}
      </div>
      <nav className="p-nav">
        {NAV.map((group) => (
          <Fragment key={group.groupKey ?? 'root'}>
            {group.groupKey && !collapsed && <div className="p-navgroup">{t(group.groupKey)}</div>}
            {group.groupKey && collapsed && <div className="p-divider" style={{ margin: '8px 6px' }} />}
            {group.items.map((item) => (
              <NavButton
                key={item.key}
                item={item}
                collapsed={collapsed}
                active={(route === '/policies' ? '/plans' : route) === item.key}
                count={item.count ? counts[item.count] : undefined}
                onClick={() => onNavigate(item.key)}
              />
            ))}
          </Fragment>
        ))}
      </nav>
      <div className="p-railfoot">
        {collapsed ? (
          <Tooltip title={version} placement="right">
            <a className="p-navitem" href={REPO_URL} target="_blank" rel="noopener noreferrer" style={{ color: 'var(--p-text-3)' }}>
              <span className="p-navicon"><Icon name="github" size={15} /></span>
            </a>
          </Tooltip>
        ) : (
          <a className="p-navitem" href={REPO_URL} target="_blank" rel="noopener noreferrer" style={{ color: 'var(--p-text-3)', fontSize: 11.5 }}>
            <span className="p-navicon"><Icon name="github" size={15} /></span>
            <span className="p-mono">{version}</span>
          </a>
        )}
      </div>
    </aside>
  );
}
