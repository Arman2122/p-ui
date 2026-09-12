import { useCallback, useEffect, useState } from 'react';
import type { CSSProperties } from 'react';
import { Navigate, Outlet, useLocation, useNavigate } from 'react-router';
import { Alert, Button, ConfigProvider, Spin } from 'antd';
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
import { useSignedInUser } from '@/pages/resellers/api';
import ResellerDashboardPage, { ResellerInboundsPage } from '@/pages/resellers/ResellerDashboardPage';
import ResellerClientsPage from '@/pages/resellers/ResellerClientsPage';
import ResellerAccountPage from '@/pages/resellers/ResellerAccountPage';
import PlansPage from '@/pages/policies/PlansPage';
import type { SignedInUser } from '@/pages/resellers/schema';
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
  const me = useSignedInUser();
  const { t } = useTranslation();
  if (me.isPending) return <Spin fullscreen />;
  if (!me.data) return <Alert type="error" title={t(me.error?.message || 'resellers.loadError')} action={<Button onClick={() => { window.location.href = window.PUI_BASE_PATH || '/'; }}>{t('resellers.signIn')}</Button>} />;
  return <SignedInPanel user={me.data} />;
}

function AdminNavCounts({ onCounts }: { onCounts: (value: { cores?: number; plans?: number }) => void }) {
  const { cores } = useCoresStatusQuery();
  const policies = usePoliciesQuery();
  useEffect(() => onCounts({ cores: cores.length || undefined, plans: policies.data?.length }), [cores.length, policies.data?.length, onCounts]);
  return null;
}

function SignedInPanel({ user }: { user: SignedInUser }) {
  const admin = user.role === 'admin';
  useWebSocketBridge(admin, user.id);
  usePageTitle();
  useTokenBridge();

  const { t } = useTranslation();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const { isDark, isUltra, toggleTheme, toggleUltra, antdThemeConfig } = useTheme();
  const phone = useIsPhone();

  const [collapsed, setCollapsed] = useState(readCollapsed);
  const [drawer, setDrawer] = useState(false);

  const [counts, setCounts] = useState<{ cores?: number; plans?: number }>({});

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
    <ConfigProvider theme={antdThemeConfig}>
    <div className="p-root">
      {admin && <AdminNavCounts onCounts={setCounts} />}
      <div className="p-shell" style={shellStyle}>
        <PanelSidebar
          route={route}
          collapsed={collapsed && !phone}
          open={drawer}
          counts={counts}
          user={user}
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
            user={user}
            route={route}
            isDark={isDark}
            onToggleSidebar={onToggleSidebar}
            onToggleTheme={onToggleTheme}
            onNavigate={onNavigate}
            onLogout={() => void onLogout()}
          />
          <main className="p-page" key={route}>
            {admin ? <Outlet /> : route === '/' ? <Navigate to="/reseller" replace />
              : route === '/reseller' ? <ResellerDashboardPage />
                : route === '/clients' ? <ResellerClientsPage />
                  : route === '/inbounds' ? <ResellerInboundsPage />
                    : route === '/plans' && user.permissions.plans ? <PlansPage reseller />
                      : route === '/reseller/account' ? <ResellerAccountPage />
                        : route === '/activity' || route === '/api-docs' ? <Outlet />
                        : <Navigate to="/reseller" replace />}
          </main>
        </div>
      </div>
    </div>
    </ConfigProvider>
  );
}
