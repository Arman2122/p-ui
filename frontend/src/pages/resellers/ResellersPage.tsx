import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Spin, Table, Tag } from 'antd';
import { FormProvider, useWatch } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { Icon } from '@/components/ui/Icon';
import { Kicker, PageHeader, PanelCard } from '@/components/ui/panel';
import StatStrip from '@/components/ui/StatStrip';
import { FormField, useZodForm } from '@/components/form/rhf';
import { ClientTrendCell } from '@/pages/clients/ClientMetadataCells';
import { GiB, resellerGet, resellerKeys, resellerPost } from './api';
import { ClientPickerSchema, ResellerClientPageSchema, ResellerDeleteSchema, ResellerTopUpSchema, ResellerTransferResultSchema, ResellerViewSchema, type ResellerView } from './schema';
import ResellerBudget from './ResellerBudget';
import ResellerModal from './ResellerModal';

export default function ResellersPage() {
  const { t } = useTranslation();
  const cache = useQueryClient();
  const [search, setSearch] = useState('');
  const [editing, setEditing] = useState<ResellerView | null | false>(false);
  const [topping, setTopping] = useState<ResellerView>();
  const [deleting, setDeleting] = useState<ResellerView>();
  const [owner, setOwner] = useState<ResellerView>();
  const [page, setPage] = useState(1);
  const [clientSearch, setClientSearch] = useState('');
  const [pickerSearch, setPickerSearch] = useState('');
  const [picked, setPicked] = useState<string[]>([]);
  const [moved, setMoved] = useState<string>();
  const topForm = useZodForm(ResellerTopUpSchema, { defaultValues: { trafficGB: 100, days: 0, clients: 0 } });
  const deleteForm = useZodForm(ResellerDeleteSchema, { defaultValues: { clients: 'transfer', ownerId: 0 } });
  const disposition = useWatch({ control: deleteForm.control, name: 'clients' });
  const query = useQuery({ queryKey: resellerKeys, queryFn: () => resellerGet('/resellers', z.array(ResellerViewSchema)), refetchInterval: 30_000 });
  const grants = useQuery({ queryKey: [...resellerKeys, 'grant-options'], queryFn: () => resellerGet('/inbounds/list', z.array(z.object({ id: z.number(), remark: z.string(), protocol: z.string() }))) });
  const clients = useQuery({ queryKey: [...resellerKeys, owner?.account.userId, 'clients', page, clientSearch], queryFn: () => resellerGet(`/resellers/${owner?.account.userId}/clients`, ResellerClientPageSchema, { page, size: 50, search: clientSearch }), enabled: !!owner });
  const picker = useQuery({ queryKey: [...resellerKeys, 'picker', pickerSearch], queryFn: () => resellerGet('/clients/list/paged', ClientPickerSchema, { page: 1, pageSize: 20, search: pickerSearch }), enabled: !!owner });
  const mutation = useMutation({ mutationFn: ({ path, body }: { path: string; body: unknown }) => resellerPost(path, body), onSuccess: async () => { await cache.invalidateQueries({ queryKey: resellerKeys }); setTopping(undefined); setDeleting(undefined); } });
  const transfer = useMutation({ mutationFn: ({ path, emails }: { path: string; emails: string[] }) => resellerPost(path, { emails }), onSuccess: async (obj) => {
    const result = ResellerTransferResultSchema.safeParse(obj);
    setMoved(result.success ? t('resellers.assigned', { moved: result.data.moved, plans: result.data.plansCleared }) : undefined);
    setPicked([]);
    await cache.invalidateQueries({ queryKey: resellerKeys });
  } });
  const rows = query.data || [];
  const filtered = rows.filter((r) => `${r.account.name} ${r.username} ${r.account.email}`.toLowerCase().includes(search.toLowerCase()));
  const owned = new Set((clients.data?.items || []).map((c) => c.email));
  const closeOwner = () => { setOwner(undefined); setMoved(undefined); setPicked([]); setClientSearch(''); };
  return <>
    <PageHeader title={t('resellers.title')} sub={t('resellers.description')} actions={<Button type="primary" icon={<Icon name="plus" size={15} />} onClick={() => setEditing(null)}>{t('resellers.add')}</Button>} />
    <StatStrip items={[
      { key: 'accounts', label: t('resellers.title'), value: rows.length },
      { key: 'active', label: t('resellers.active'), value: rows.filter((r) => !r.account.suspended && !r.account.budgetBlocked).length },
      { key: 'clients', label: t('resellers.clients'), value: rows.reduce((n, r) => n + r.clients, 0) },
      { key: 'online', label: t('resellers.online'), value: rows.reduce((n, r) => n + r.online, 0), color: 'var(--p-primary)' },
      { key: 'traffic', label: t('resellers.trafficUsed'), value: `${(rows.reduce((n, r) => n + r.trafficUsed, 0) / GiB).toFixed(1)} GiB` },
    ]} />
    {(query.error || mutation.error || grants.error) && <Alert type="error" title={t((query.error || mutation.error || grants.error)?.message || 'resellers.loadError')} action={<Button onClick={() => { void query.refetch(); void grants.refetch(); }}>{t('resellers.retry')}</Button>} showIcon />}
    <Input.Search value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t('resellers.search')} allowClear style={{ maxWidth: 380 }} />
    {query.isPending ? <Spin /> : <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,320px),1fr))' }}>
      {filtered.map((r) => <PanelCard key={r.account.userId} title={<Space><span className="p-avatar">{r.account.name.slice(0, 2).toUpperCase()}</span><span><strong>{r.account.name}</strong><div className="p-sub">@{r.username} · {r.account.email}</div></span></Space>} extra={<Tag color={r.account.suspended || r.account.budgetBlocked ? 'warning' : 'success'}>{t(r.account.suspended ? 'resellers.suspended' : r.account.budgetBlocked ? 'resellers.depleted' : 'resellers.active')}</Tag>}>
        <ResellerBudget reseller={r} />
        <div className="p-grid" style={{ gridTemplateColumns: 'auto auto 1fr', alignItems: 'end', gap: 16, marginTop: 16 }}>
          <div><Kicker>{t('resellers.online')}</Kicker><strong data-testid={`online-${r.account.userId}`}>{r.online}</strong></div>
          <div><Kicker>{t('resellers.active')}</Kicker><strong>{r.active}</strong></div>
          <ClientTrendCell wide client={{ email: r.username, history: r.history ?? [] }} hint={t('resellers.usageTrend')} />
        </div>
        <div style={{ marginBlock: 16 }}><div className="p-sub">{t('resellers.grantsCount', { count: r.inboundIds.length })}</div><Space wrap size={4} style={{ marginTop: 6 }}>{r.inboundIds.map((id) => <Tag key={id}>{grants.data?.find((i) => i.id === id)?.remark || `#${id}`}</Tag>)}{r.account.allowPlans && <Tag>{t('resellers.allowPlans')}</Tag>}</Space></div>
        <Space wrap>
          <Button size="small" icon={<Icon name="edit" size={14} />} onClick={() => setEditing(r)}>{t('edit')}</Button>
          <Button size="small" onClick={() => { setTopping(r); topForm.reset({ trafficGB: r.account.trafficLimit ? 100 : 0, days: 0, clients: 0 }); }}>{t('resellers.topUp')}</Button>
          <Button size="small" onClick={() => { setOwner(r); setPage(1); }}>{t('resellers.clients')}</Button>
          <Popconfirm title={t('resellers.resetUsageConfirm')} okText={t('resellers.resetUsage')} onConfirm={() => mutation.mutateAsync({ path: `/resellers/${r.account.userId}/reset-usage`, body: {} })}>
            <Button size="small" icon={<Icon name="refresh" size={14} />} loading={mutation.isPending}>{t('resellers.resetUsage')}</Button>
          </Popconfirm>
          <Popconfirm title={t(r.account.suspended ? 'resellers.reactivateConfirm' : 'resellers.suspendConfirm')} onConfirm={() => mutation.mutateAsync({ path: `/resellers/${r.account.userId}/suspend`, body: { suspended: !r.account.suspended } })}>
            <Button size="small" danger={!r.account.suspended} loading={mutation.isPending}>{t(r.account.suspended ? 'resellers.reactivate' : 'resellers.suspend')}</Button>
          </Popconfirm>
          <Button size="small" danger type="text" icon={<Icon name="trash" size={14} />} aria-label={`${t('resellers.deleteReseller')}: ${r.username}`} onClick={() => { setDeleting(r); deleteForm.reset({ clients: 'transfer', ownerId: 0 }); }}>{t('delete')}</Button>
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
    <Modal open={!!deleting} title={`${t('resellers.deleteReseller')} · ${deleting?.account.name || ''}`} onCancel={() => setDeleting(undefined)} okText={t('delete')} okButtonProps={{ danger: true }} confirmLoading={mutation.isPending}
      onOk={() => void deleteForm.handleSubmit((v) => mutation.mutate({ path: `/resellers/${deleting?.account.userId}/delete`, body: { clients: v.clients, ownerId: v.clients === 'transfer' ? v.ownerId : 0 } }))()}>
      {mutation.error && <Alert type="error" title={t(mutation.error.message)} />}
      <p className="p-sub" style={{ whiteSpace: 'normal' }}>{t('resellers.deleteHelp', { count: deleting?.clients ?? 0 })}</p>
      <FormProvider {...deleteForm}><Form layout="vertical" component="div">
        <FormField name="clients" label={t('resellers.clients')}><Segmented block options={[{ value: 'transfer', label: t('resellers.transferClients') }, { value: 'delete', label: t('resellers.deleteClients') }]} /></FormField>
        {disposition === 'transfer' && <FormField name="ownerId" label={t('resellers.transferTo')}><Select options={[{ value: 0, label: t('resellers.administrator') }, ...rows.filter((r) => r.account.userId !== deleting?.account.userId).map((r) => ({ value: r.account.userId, label: `${r.account.name} · @${r.username}` }))]} /></FormField>}
      </Form></FormProvider>
    </Modal>
    <Modal open={!!owner} title={`${t('resellers.clients')} · ${owner?.account.name || ''}`} onCancel={closeOwner} footer={null} width={900}>
      {(clients.error || transfer.error || picker.error) && <Alert type="error" title={t((clients.error || transfer.error || picker.error)?.message || 'resellers.loadError')} />}
      {moved && <Alert type="success" title={moved} closable onClose={() => setMoved(undefined)} />}
      <div className="p-grid" style={{ gridTemplateColumns: '1fr auto', alignItems: 'start', marginBottom: 12 }}>
        <Select mode="multiple" showSearch value={picked} onChange={setPicked} onSearch={setPickerSearch} filterOption={false} loading={picker.isFetching} placeholder={t('resellers.assignPlaceholder')} notFoundContent={null}
          options={(picker.data?.items || []).filter((c) => !owned.has(c.email)).map((c) => ({ value: c.email, label: c.email }))} aria-label={t('resellers.assignExisting')} />
        <Button type="primary" disabled={!picked.length} loading={transfer.isPending} onClick={() => transfer.mutate({ path: `/resellers/${owner?.account.userId}/clients/assign`, emails: picked })}>{t('resellers.assign')}</Button>
      </div>
      <Input.Search value={clientSearch} onChange={(e) => { setClientSearch(e.target.value); setPage(1); }} placeholder={t('resellers.search')} allowClear style={{ maxWidth: 360, marginBottom: 12 }} />
      <Table rowKey="id" size="small" loading={clients.isPending} dataSource={clients.data?.items} pagination={{ current: page, pageSize: 50, total: clients.data?.total, onChange: setPage, showSizeChanger: false }} columns={[
        { title: t('resellers.clientName'), dataIndex: 'email' },
        { title: t('resellers.trafficUsed'), render: (_, r) => `${(r.trafficUsed / GiB).toFixed(1)} GiB` },
        { title: t('resellers.expiry'), render: (_, r) => r.expiryTime ? new Date(r.expiryTime).toLocaleDateString() : '∞' },
        { title: t('resellers.active'), render: (_, r) => t(r.enable ? 'resellers.active' : 'resellers.suspended') },
        { title: t('resellers.actions'), align: 'end', render: (_, r) => <Popconfirm title={t('resellers.releaseConfirm')} okText={t('resellers.release')} onConfirm={() => transfer.mutateAsync({ path: `/resellers/${owner?.account.userId}/clients/release`, emails: [r.email] })}>
          <Button size="small" type="text" aria-label={`${t('resellers.release')}: ${r.email}`}>{t('resellers.release')}</Button></Popconfirm> },
      ]} />
    </Modal>
  </>;
}
