import { useCallback, useEffect, useState } from 'react';
import type { CSSProperties } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';

import { useWebSocketBridge } from '@/api/websocketBridge';
import { usePageTitle } from '@/hooks/usePageTitle';
import { useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import { usePoliciesQuery } from '@/api/queries/usePoliciesQuery';
import { HttpUtil } from '@/utils';
import PanelSidebar from '@/layouts/PanelSidebar';
import PanelTopBar from '@/layouts/PanelTopBar';
import '@/styles/panel.css';

const COLLAPSED_KEY = 'panel-rail-collapsed';
const PHONE_QUERY = '(max-width: 760px)';

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === 'true';
  } catch {
    return false;
  }
}

function useIsPhone(): boolean {
  const [phone, setPhone] = useState(() => window.matchMedia(PHONE_QUERY).matches);
  useEffect(() => {
    const mq = window.matchMedia(PHONE_QUERY);
    const check = () => setPhone(mq.matches);
    check();
    mq.addEventListener('change', check);
    return () => mq.removeEventListener('change', check);
  }, []);
  return phone;
}

export default function PanelLayout() {
  useWebSocketBridge();
  usePageTitle();
  useTokenBridge();

  const { t } = useTranslation();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const { isDark, isUltra, toggleTheme, toggleUltra } = useTheme();
  const phone = useIsPhone();

  const [collapsed, setCollapsed] = useState(readCollapsed);
  const [drawer, setDrawer] = useState(false);

  const { cores } = useCoresStatusQuery();
  const policies = usePoliciesQuery();

  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSED_KEY, String(collapsed));
    } catch {
      /* a browser refusing storage must not break the shell */
    }
  }, [collapsed]);

  useEffect(() => {
    if (!phone) setDrawer(false);
  }, [phone]);

  const route = pathname === '' ? '/' : pathname;

  const onNavigate = useCallback((key: string) => {
    navigate(key);
    setDrawer(false);
  }, [navigate]);

  const onLogout = useCallback(async () => {
    await HttpUtil.post('/logout');
    window.location.href = window.PUI_BASE_PATH || '/';
  }, []);

  /* The rail collapses on a wide screen and slides in as a drawer on a phone,
     so the same control means "give me the nav" at every width. */
  const onToggleSidebar = useCallback(() => {
    if (phone) setDrawer((v) => !v);
    else setCollapsed((v) => !v);
  }, [phone]);

  const onToggleTheme = useCallback(() => {
    if (isUltra) toggleUltra();
    toggleTheme();
  }, [isUltra, toggleTheme, toggleUltra]);

  const shellStyle = { '--rail': collapsed ? '64px' : '228px' } as CSSProperties;

  return (
    <div className="p-root">
      <div className="p-shell" style={shellStyle}>
        <PanelSidebar
          route={route}
          collapsed={collapsed && !phone}
          open={drawer}
          counts={{ cores: cores.length || undefined, plans: policies.data?.length }}
          onNavigate={onNavigate}
        />
        {drawer && (
          <button
            type="button"
            className="p-backdrop"
            aria-label={t('close')}
            onClick={() => setDrawer(false)}
          />
        )}
        <div className="p-main">
          <PanelTopBar
            route={route}
            isDark={isDark}
            onToggleSidebar={onToggleSidebar}
            onToggleTheme={onToggleTheme}
            onNavigate={onNavigate}
            onLogout={() => void onLogout()}
          />
          <main className="p-page" key={route}>
            <Outlet />
          </main>
        </div>
      </div>
    </div>
  );
}
