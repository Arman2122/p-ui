import { lazy, Suspense } from 'react';
import { createBrowserRouter, type RouteObject } from 'react-router';
import { Spin } from 'antd';

import PanelLayout from '@/layouts/PanelLayout';

const IndexPage = lazy(() => import('@/pages/index/IndexPage'));
const InboundsPage = lazy(() => import('@/pages/inbounds/InboundsPage'));
const ClientsPage = lazy(() => import('@/pages/clients/ClientsPage'));
const GroupsPage = lazy(() => import('@/pages/groups/GroupsPage'));
const PoliciesPage = lazy(() => import('@/pages/policies/PlansPage'));
const NodesPage = lazy(() => import('@/pages/nodes/NodesPage'));
const CoresPage = lazy(() => import('@/pages/cores/CoresPage'));
const HostsPage = lazy(() => import('@/pages/hosts/HostsPage'));
const SettingsPage = lazy(() => import('@/pages/settings/SettingsPage'));
const OutboundsPage = lazy(() => import('@/pages/outbounds/OutboundsPage'));
const RoutingPage = lazy(() => import('@/pages/routing/RoutingPage'));
const XrayPage = lazy(() => import('@/pages/xray/XrayPage'));
const ApiDocsPage = lazy(() => import('@/pages/api-docs/ApiExplorer'));
const ActivityPage = lazy(() => import('@/pages/activity/ActivityPage'));
const ResellersPage = lazy(() => import('@/pages/resellers/ResellersPage'));
const ResellerDashboardPage = lazy(() => import('@/pages/resellers/ResellerDashboardPage'));
const ResellerAccountPage = lazy(() => import('@/pages/resellers/ResellerAccountPage'));

function withSuspense(node: React.ReactNode) {
  return <Suspense fallback={<div style={{ display: 'grid', placeItems: 'center', minHeight: 160 }}><Spin size="large" /></div>}>{node}</Suspense>;
}

const routes: RouteObject[] = [
  {
    path: '/',
    element: <PanelLayout />,
    children: [
      { index: true, element: withSuspense(<IndexPage />) },
      { path: 'inbounds', element: withSuspense(<InboundsPage />) },
      { path: 'clients', element: withSuspense(<ClientsPage />) },
      { path: 'groups', element: withSuspense(<GroupsPage />) },
      { path: 'policies', element: withSuspense(<PoliciesPage />) },
      { path: 'plans', element: withSuspense(<PoliciesPage />) },
      { path: 'nodes', element: withSuspense(<NodesPage />) },
      { path: 'cores', element: withSuspense(<CoresPage />) },
      { path: 'hosts', element: withSuspense(<HostsPage />) },
      { path: 'settings', element: withSuspense(<SettingsPage />) },
      { path: 'xray', element: withSuspense(<XrayPage />) },
      { path: 'outbound', element: withSuspense(<OutboundsPage />) },
      { path: 'outbounds', element: withSuspense(<OutboundsPage />) },
      { path: 'routing', element: withSuspense(<RoutingPage />) },
      { path: 'api-docs', element: withSuspense(<ApiDocsPage />) },
      { path: 'activity', element: withSuspense(<ActivityPage />) },
      { path: 'resellers', element: withSuspense(<ResellersPage />) },
      { path: 'reseller', element: withSuspense(<ResellerDashboardPage />) },
      { path: 'reseller/account', element: withSuspense(<ResellerAccountPage />) },
    ],
  },
];

function computeBasename() {
  const raw = (typeof window !== 'undefined' && window.PUI_BASE_PATH) || '/';
  const trimmed = raw.replace(/\/+$/, '');
  return `${trimmed}/panel`;
}

export const router = createBrowserRouter(routes, {
  basename: computeBasename(),
});
