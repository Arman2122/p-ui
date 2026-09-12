import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Dropdown } from 'antd';
import type { MenuProps } from 'antd';

import { Icon } from '@/components/ui/Icon';
import { navItemOf, sectionKeyOf } from '@/layouts/nav';
import CoreStatusPopover from '@/layouts/CoreStatusPopover';
import NotificationsPopover from '@/layouts/NotificationsPopover';
import type { SignedInUser } from '@/pages/resellers/schema';

export default function PanelTopBar({ route, isDark, user, onToggleSidebar, onToggleTheme, onNavigate, onLogout }: {
  route: string;
  isDark: boolean;
  user: SignedInUser;
  onToggleSidebar: () => void;
  onToggleTheme: () => void;
  onNavigate: (key: string) => void;
  onLogout: () => void;
}) {
  const { t } = useTranslation();
  const item = navItemOf(route);
  const label = route === '/reseller/account' ? t('resellers.account') : item ? t(item.labelKey) : t('menu.dashboard');
  const sectionKey = sectionKeyOf(route);
  const username = user.username;
  const admin = user.role === 'admin';
  const initial = (username.trim()[0] || 'A').toUpperCase();

  const menu: MenuProps = {
    items: [
      {
        key: 'who',
        disabled: true,
        label: (
          <span style={{ display: 'block', padding: '2px 0', minWidth: 208 }}>
            <span style={{ display: 'block', fontSize: 13, fontWeight: 650, color: 'var(--p-text)' }}>{username || t('menu.account')}</span>
            <span className="p-sub" style={{ display: 'block', marginTop: 1 }}>{t(admin ? 'menu.administrator' : 'resellers.role')}</span>
          </span>
        ),
      },
      { type: 'divider' },
      { key: admin ? '/settings#security' : '/reseller/account', label: t('menu.accountSettings'), icon: <Icon name="user" size={14} /> },
      { key: '/activity', label: t('activity.history'), icon: <Icon name="bell" size={14} /> },
      { type: 'divider' },
      { key: '__logout__', danger: true, label: t('logout'), icon: <Icon name="logout" size={14} /> },
    ],
    onClick: ({ key }) => {
      if (key === '__logout__') return onLogout();
      onNavigate(String(key));
    },
  };

  const goToCores = useCallback(() => onNavigate('/cores'), [onNavigate]);
  const goToDashboard = useCallback(() => onNavigate(user.home), [onNavigate, user.home]);

  return (
    <header className="p-top">
      <button type="button" className="p-iconbtn" onClick={onToggleSidebar} title={t('menu.toggleSidebar')} aria-label={t('menu.toggleSidebar')}>
        <Icon name="panelLeft" />
      </button>
      <div className="p-crumb">
        {sectionKey && <><span>{t(sectionKey)}</span><Icon name="chevron" size={13} /></>}
        <span style={{ color: 'var(--p-text)', fontWeight: 600 }}>{label}</span>
      </div>
      <div style={{ flex: 1 }} />
      {admin && <div className="p-only-wide">
        <CoreStatusPopover onManage={goToCores} />
        <div className="p-vsep" style={{ height: 22, alignSelf: 'center' }} />
      </div>}
      <Dropdown trigger={['click']} placement="bottomRight" menu={menu}>
        <button type="button" className="p-account">
          <span className="p-account-av">{initial}</span>
          <span className="p-account-name">{username || t('menu.account')}</span>
          <Icon name="chevronDown" size={13} />
        </button>
      </Dropdown>
      <NotificationsPopover onOpenDashboard={goToDashboard} />
      <button
        type="button"
        className="p-iconbtn"
        onClick={onToggleTheme}
        title={isDark ? t('menu.lightTheme') : t('menu.darkTheme')}
        aria-label={isDark ? t('menu.lightTheme') : t('menu.darkTheme')}
      >
        <Icon name={isDark ? 'sun' : 'moon'} />
      </button>
    </header>
  );
}
