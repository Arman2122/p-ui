import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Form, Input, InputNumber, Modal, Popconfirm, Space, Spin, Table, Tag } from 'antd';
import { FormProvider } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { Icon } from '@/components/ui/Icon';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import StatStrip from '@/components/ui/StatStrip';
import { FormField, useZodForm } from '@/components/form/rhf';
import { GiB, resellerGet, resellerKeys, resellerPost } from './api';
import { ResellerClientPageSchema, ResellerTopUpSchema, ResellerViewSchema, type ResellerView } from './schema';
import ResellerBudget from './ResellerBudget';
import ResellerModal from './ResellerModal';

export default function ResellersPage() {
  const { t } = useTranslation();
  const cache = useQueryClient();
  const [search, setSearch] = useState('');
  const [editing, setEditing] = useState<ResellerView | null | false>(false);
  const [topping, setTopping] = useState<ResellerView>();
  const [owner, setOwner] = useState<ResellerView>();
  const [page, setPage] = useState(1);
  const topForm = useZodForm(ResellerTopUpSchema, { defaultValues: { trafficGB: 100, days: 0, clients: 0 } });
  const query = useQuery({ queryKey: resellerKeys, queryFn: () => resellerGet('/resellers', z.array(ResellerViewSchema)), refetchInterval: 30_000 });
  const grants = useQuery({ queryKey: [...resellerKeys, 'grant-options'], queryFn: () => resellerGet('/inbounds/list', z.array(z.object({ id: z.number(), remark: z.string(), protocol: z.string() }))) });
  const clients = useQuery({ queryKey: [...resellerKeys, owner?.account.userId, 'clients', page], queryFn: () => resellerGet(`/resellers/${owner?.account.userId}/clients`, ResellerClientPageSchema, { page, size: 50 }), enabled: !!owner });
  const mutation = useMutation({ mutationFn: ({ path, body }: { path: string; body: unknown }) => resellerPost(path, body), onSuccess: async () => { await cache.invalidateQueries({ queryKey: resellerKeys }); setTopping(undefined); } });
  const rows = query.data || [];
  const filtered = rows.filter((r) => `${r.account.name} ${r.username} ${r.account.email}`.toLowerCase().includes(search.toLowerCase()));
  return <>
    <PageHeader title={t('resellers.title')} sub={t('resellers.description')} actions={<Button type="primary" icon={<Icon name="plus" size={15} />} onClick={() => setEditing(null)}>{t('resellers.add')}</Button>} />
    <StatStrip items={[
      { key: 'accounts', label: t('resellers.title'), value: rows.length },
      { key: 'active', label: t('resellers.active'), value: rows.filter((r) => !r.account.suspended && !r.account.budgetBlocked).length },
      { key: 'clients', label: t('resellers.clients'), value: rows.reduce((n, r) => n + r.clients, 0) },
      { key: 'traffic', label: t('resellers.trafficUsed'), value: `${(rows.reduce((n, r) => n + r.trafficUsed, 0) / GiB).toFixed(1)} GiB` },
    ]} />
    {(query.error || mutation.error || grants.error) && <Alert type="error" title={t((query.error || mutation.error || grants.error)?.message || 'resellers.loadError')} action={<Button onClick={() => { void query.refetch(); void grants.refetch(); }}>{t('resellers.retry')}</Button>} showIcon />}
    <Input.Search value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t('resellers.search')} allowClear style={{ maxWidth: 380 }} />
    {query.isPending ? <Spin /> : <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,320px),1fr))' }}>
      {filtered.map((r) => <PanelCard key={r.account.userId} title={<Space><span className="p-avatar">{r.account.name.slice(0, 2).toUpperCase()}</span><span><strong>{r.account.name}</strong><div className="p-sub">@{r.username} · {r.account.email}</div></span></Space>} extra={<Tag color={r.account.suspended || r.account.budgetBlocked ? 'warning' : 'success'}>{t(r.account.suspended ? 'resellers.suspended' : r.account.budgetBlocked ? 'resellers.depleted' : 'resellers.active')}</Tag>}>
        <ResellerBudget reseller={r} />
        <div style={{ marginBlock: 16 }}><div className="p-sub">{t('resellers.grantsCount', { count: r.inboundIds.length })}</div><Space wrap size={4} style={{ marginTop: 6 }}>{r.inboundIds.map((id) => <Tag key={id}>{grants.data?.find((i) => i.id === id)?.remark || `#${id}`}</Tag>)}{r.account.allowPlans && <Tag>{t('resellers.allowPlans')}</Tag>}</Space></div>
        <Space wrap>
          <Button size="small" icon={<Icon name="edit" size={14} />} onClick={() => setEditing(r)}>{t('edit')}</Button>
          <Button size="small" onClick={() => { setTopping(r); topForm.reset({ trafficGB: r.account.trafficLimit ? 100 : 0, days: 0, clients: 0 }); }}>{t('resellers.topUp')}</Button>
          <Button size="small" onClick={() => { setOwner(r); setPage(1); }}>{t('resellers.clients')}</Button>
          <Popconfirm title={t(r.account.suspended ? 'resellers.reactivateConfirm' : 'resellers.suspendConfirm')} onConfirm={() => mutation.mutateAsync({ path: `/resellers/${r.account.userId}/suspend`, body: { suspended: !r.account.suspended } })}>
            <Button size="small" danger={!r.account.suspended} loading={mutation.isPending}>{t(r.account.suspended ? 'resellers.reactivate' : 'resellers.suspend')}</Button>
          </Popconfirm>
        </Space>
      </PanelCard>)}
    </div>}
    {!query.isPending && !filtered.length && <Empty description={t('resellers.empty')} />}
    <ResellerModal open={editing !== false} reseller={editing || undefined} onClose={() => setEditing(false)} />
    <Modal open={!!topping} title={t('resellers.topUp')} onCancel={() => setTopping(undefined)} onOk={() => void topForm.handleSubmit((v) => mutation.mutate({ path: `/resellers/${topping?.account.userId}/top-up`, body: { traffic: Math.round(v.trafficGB * GiB), days: v.days, clients: v.clients } }))()} confirmLoading={mutation.isPending}>
      {mutation.error && <Alert type="error" title={t(mutation.error.message)} />}
      <FormProvider {...topForm}><Form layout="vertical" component="div">
        <FormField name="trafficGB" label={`${t('resellers.traffic')} · GiB`}><InputNumber min={0} disabled={!topping?.account.trafficLimit} /></FormField>
        <FormField name="days" label={t('resellers.additionalDays')}><InputNumber min={0} precision={0} disabled={!topping?.account.expiresAt} /></FormField>
        <FormField name="clients" label={t('resellers.additionalClients')}><InputNumber min={0} precision={0} disabled={!topping?.account.clientCap} /></FormField>
      </Form></FormProvider>
    </Modal>
    <Modal open={!!owner} title={`${t('resellers.clients')} · ${owner?.account.name || ''}`} onCancel={() => setOwner(undefined)} footer={null} width={850}>
      {clients.error && <Alert type="error" title={t(clients.error.message)} />}
      <Table rowKey="id" loading={clients.isPending} dataSource={clients.data?.items} pagination={{ current: page, pageSize: 50, total: clients.data?.total, onChange: setPage, showSizeChanger: false }} columns={[
        { title: t('resellers.clientName'), dataIndex: 'email' },
        { title: t('resellers.trafficUsed'), render: (_, r) => `${(r.trafficUsed / GiB).toFixed(1)} GiB` },
        { title: t('resellers.expiry'), render: (_, r) => r.expiryTime ? new Date(r.expiryTime).toLocaleDateString() : '∞' },
        { title: t('resellers.active'), render: (_, r) => t(r.enable ? 'resellers.active' : 'resellers.suspended') },
      ]} />
    </Modal>
  </>;
}
