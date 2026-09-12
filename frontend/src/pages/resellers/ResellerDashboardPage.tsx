import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Empty, Input, Space, Spin, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import StatStrip from '@/components/ui/StatStrip';
import Sparkline from '@/components/viz/Sparkline';
import { exportOwnedClients, GiB, resellerGet, resellerKeys, useSignedInUser } from './api';
import { ResellerDashboardSchema, ResellerInboundSchema } from './schema';
import ResellerBudget from './ResellerBudget';

export default function ResellerDashboardPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const me = useSignedInUser().data;
  const exportMutation = useMutation({ mutationFn: exportOwnedClients });
  const q = useQuery({ queryKey: [...resellerKeys, 'dashboard'], queryFn: () => resellerGet('/reseller/dashboard', ResellerDashboardSchema), refetchInterval: 15_000 });
  const plans = useQuery({ queryKey: [...resellerKeys, 'my-plan-choices'], queryFn: () => resellerGet('/reseller/plans/list', z.array(z.object({ id: z.number(), name: z.string(), archived: z.boolean().optional() }))), enabled: !!me?.permissions.plans });
  if (q.isPending) return <Spin />;
  if (!q.data) return <Alert type="error" title={t(q.error?.message || 'resellers.loadError')} action={<Button onClick={() => void q.refetch()}>{t('resellers.retry')}</Button>} />;
  const d = q.data;
  return <>
    <PageHeader title={t('menu.dashboard')} sub={`${d.reseller.account.name} · @${d.reseller.username} · ${t('resellers.role')}`} actions={<Space><Button loading={exportMutation.isPending} onClick={() => exportMutation.mutate()}>{t('resellers.export')}</Button><Button type="primary" onClick={() => navigate('/clients?new=1')}>{t('resellers.newClient')}</Button></Space>} />
    {exportMutation.error && <Alert type="error" title={t(exportMutation.error.message)} />}
    <StatStrip items={[
      { key: 'clients', label: t('resellers.clients'), value: d.reseller.clients }, { key: 'online', label: t('resellers.online'), value: d.online, color: 'var(--p-primary)' },
      { key: 'active', label: t('resellers.active'), value: d.active }, { key: 'attention', label: t('resellers.attention'), value: d.attention.length },
      { key: 'traffic', label: t('resellers.trafficUsed'), value: `${(d.reseller.trafficUsed / GiB).toFixed(1)} GiB` },
    ]} />
    {d.reseller.account.budgetBlocked && <Alert type="warning" title={t('resellers.budgetBlocked')} showIcon />}
    <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,300px),1fr))' }}>
      <PanelCard kicker={t('resellers.yourBudget')}><ResellerBudget reseller={d.reseller} /><p className="p-sub" style={{ whiteSpace: 'normal' }}>{t('resellers.budgetExplanation')}</p></PanelCard>
      <PanelCard kicker={t('resellers.salesHistory')}>
        {d.sales.length ? <Sparkline data={d.sales.map((x) => x.bytes / GiB)} labels={d.sales.map((x) => x.day)} height={180} name1="GiB" showAxes showGrid /> : <Empty description={t('resellers.noSales')} />}
        <p className="p-sub">{t('resellers.allocated')}: {(d.reseller.account.trafficAllocated / GiB).toFixed(1)} GiB</p>
      </PanelCard>
      <PanelCard kicker={t('resellers.attention')} extra={<Button size="small" type="text" onClick={() => navigate('/clients')}>{t('resellers.allClients')}</Button>}>
        {d.attention.length ? d.attention.map((c) => <div className="p-alert" key={c.id}><div style={{ flex: 1 }}><strong>{c.email}</strong><div className="p-sub">{(c.trafficUsed / GiB).toFixed(1)} GiB · {c.expiryTime ? new Date(c.expiryTime).toLocaleDateString() : '∞'}</div></div><Button size="small" onClick={() => navigate(`/clients?search=${encodeURIComponent(c.email)}`)}>{t('edit')}</Button></div>) : <Empty description={t('resellers.noAttention')} />}
      </PanelCard>
    </div>
    <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,320px),1fr))' }}>
      {me?.permissions.plans && <PanelCard kicker={t('resellers.yourPlans')} extra={<Button size="small" onClick={() => navigate('/plans')}>{t('resellers.manage')}</Button>}>
        {plans.error && <Alert type="error" title={t(plans.error.message)} />}
        {(plans.data || []).filter((p) => !p.archived).map((p) => <div key={p.id} className="p-alert"><strong style={{ flex: 1 }}>{p.name}</strong><Button size="small" onClick={() => navigate(`/clients?new=1&plan=${p.id}`)}>{t('resellers.usePlan')}</Button></div>)}
        {!plans.isPending && !plans.data?.length && <Empty description={t('resellers.noPlans')} />}
      </PanelCard>}
      <PanelCard kicker={t('resellers.availableInbounds')} extra={<Button size="small" onClick={() => navigate('/inbounds')}>{t('resellers.viewAll')}</Button>}>
        {d.inbounds.map((i) => <div className="p-alert" key={i.id}><strong style={{ flex: 1 }}>{i.remark}</strong><Tag>{i.protocol}</Tag><span className="p-sub">{t('resellers.clientCount', { count: i.clients })}</span></div>)}
        <p className="p-sub" style={{ whiteSpace: 'normal' }}>{t('resellers.readonlyInbounds')}</p>
      </PanelCard>
    </div>
  </>;
}

export function ResellerInboundsPage() {
  const { t } = useTranslation(); const navigate = useNavigate();
  const [search, setSearch] = useState('');
  const q = useQuery({ queryKey: [...resellerKeys, 'my-inbounds'], queryFn: () => resellerGet('/reseller/inbounds', z.array(ResellerInboundSchema)) });
  return <><PageHeader title={t('menu.inbounds')} sub={t('resellers.readonlyInbounds')} />
    <Input.Search value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t('resellers.search')} allowClear style={{ maxWidth: 360 }} />
    {q.error && <Alert type="error" title={t(q.error.message)} />}
    {q.isPending ? <Spin /> : <PanelCard pad={0}>{q.data?.filter((i) => `${i.remark} ${i.protocol}`.toLowerCase().includes(search.toLowerCase())).map((i) => <div className="p-alert" key={i.id}><strong style={{ flex: 1 }}>{i.remark}</strong><Tag>{i.protocol}</Tag><span>{t('resellers.clientCount', { count: i.clients })}</span><span className="p-sub">{(i.trafficUsed / GiB).toFixed(1)} GiB</span><Button disabled={!i.enable} onClick={() => navigate(`/clients?new=1&inbound=${i.id}`)}>{t('resellers.newClient')}</Button></div>)}</PanelCard>}
  </>;
}
