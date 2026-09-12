import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FormProvider, useWatch } from 'react-hook-form';
import { useSearchParams } from 'react-router';
import { Alert, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tooltip, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import dayjs from 'dayjs';
import { PageHeader, StatusPip } from '@/components/ui/panel';
import { Icon } from '@/components/ui/Icon';
import { FormField, useZodForm } from '@/components/form/rhf';
import DateTimePicker from '@/components/form/DateTimePicker';
import ClientTrafficCell from '@/components/clients/ClientTrafficCell';
import { ClientPlanCell, ClientTrendCell } from '@/pages/clients/ClientMetadataCells';
import { ClientInboundChips } from '@/pages/clients/RowCells';
import { useDatepicker } from '@/hooks/useDatepicker';
import { useRelativeTime } from '@/hooks/useRelativeTime';
import { IntlUtil } from '@/utils';
import { exportOwnedClients, GiB, resellerGet, resellerKeys, resellerPost, useSignedInUser } from './api';
import { ResellerClientFormSchema, ResellerClientPageSchema, ResellerInboundSchema, type ResellerClient, type ResellerClientForm } from './schema';
import './ResellerClientsPage.css';

const PlanChoices = z.array(z.object({ id: z.number(), name: z.string(), archived: z.boolean().optional(), quotaBytes: z.number().nullish(), durationDays: z.number().nullish(), limitIp: z.number().nullish(), inboundIds: z.array(z.number()).nullish() }));
const blank = (): ResellerClientForm => ({ email: '', trafficGB: 20, expiryTime: Date.now() + 30 * 86_400_000, limitIp: 2, enable: true, comment: '', inboundIds: [], planId: 0 });

export default function ResellerClientsPage() {
  const { t } = useTranslation();
  const { datepicker } = useDatepicker();
  const relative = useRelativeTime();
  const me = useSignedInUser().data;
  const cache = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState(params.get('search') || '');
  const [editing, setEditing] = useState<ResellerClient | null | false>(false);
  const [links, setLinks] = useState<string[]>();
  const form = useZodForm(ResellerClientFormSchema, { defaultValues: blank() });
  const query = useQuery({ queryKey: [...resellerKeys, 'my-clients', page, search], queryFn: () => resellerGet('/reseller/clients', ResellerClientPageSchema, { page, size: 50, search }), refetchInterval: 30_000 });
  const inbounds = useQuery({ queryKey: [...resellerKeys, 'my-inbounds'], queryFn: () => resellerGet('/reseller/inbounds', z.array(ResellerInboundSchema)) });
  const granted = useMemo(() => Object.fromEntries((inbounds.data || []).map((ib) => [ib.id, { id: ib.id, remark: ib.remark, protocol: ib.protocol }])), [inbounds.data]);
  const plans = useQuery({ queryKey: [...resellerKeys, 'my-provisioning-plans'], queryFn: () => resellerGet('/reseller/plans/list', PlanChoices), enabled: !!me?.permissions.plans });
  const planId = useWatch({ control: form.control, name: 'planId' });
  const selectedPlan = plans.data?.find((p) => p.id === planId);
  useEffect(() => {
    if (editing !== null || !selectedPlan) return;
    if (selectedPlan.quotaBytes != null) form.setValue('trafficGB', selectedPlan.quotaBytes / GiB);
    if (selectedPlan.durationDays != null) form.setValue('expiryTime', selectedPlan.durationDays ? Date.now() + selectedPlan.durationDays * 86_400_000 : 0);
    if (selectedPlan.limitIp != null) form.setValue('limitIp', selectedPlan.limitIp);
    if (selectedPlan.inboundIds?.length) form.setValue('inboundIds', selectedPlan.inboundIds);
  }, [editing, selectedPlan, form]);
  const mutation = useMutation({ mutationFn: ({ path, body }: { path: string; body?: unknown }) => resellerPost(path, body), onSuccess: (obj) => { setEditing(false); const result = z.object({ pending: z.boolean().optional() }).passthrough().safeParse(obj); setPending(result.success && !!result.data.pending); }, onSettled: () => cache.invalidateQueries({ queryKey: resellerKeys }) });
  const [pending, setPending] = useState(false);
  const linkMutation = useMutation({ mutationFn: (id: number) => resellerGet(`/reseller/clients/${id}/links`, z.array(z.string())), onSuccess: setLinks });
  const exportMutation = useMutation({ mutationFn: exportOwnedClients });
  useEffect(() => {
    if (params.get('new') !== '1') return;
    form.reset({ ...blank(), planId: Number(params.get('plan')) || 0, inboundIds: params.get('inbound') ? [Number(params.get('inbound'))] : [] }); setEditing(null); setParams({}, { replace: true });
  }, [params, setParams, form]);
  const edit = (row: ResellerClient | null) => { form.reset(row ? { ...row, trafficGB: row.totalGB / GiB, inboundIds: row.inboundIds || [], planId: 0 } : blank()); setEditing(row); };
  const err = query.error || mutation.error || linkMutation.error || exportMutation.error || inbounds.error || plans.error;
  const online = (r: ResellerClient) => r.enable && r.lastOnline > Date.now() - 30_000;
  const expiry = (r: ResellerClient) => r.expiryTime ? IntlUtil.formatDate(r.expiryTime, datepicker) : '∞';
  return <div className="reseller-clients-page">
    <PageHeader title={t('resellers.clients')} sub={t('resellers.ownedClients')} actions={<Space><Button loading={exportMutation.isPending} onClick={() => exportMutation.mutate()}>{t('resellers.export')}</Button><Button type="primary" icon={<Icon name="plus" size={15} />} onClick={() => edit(null)}>{t('resellers.newClient')}</Button></Space>} />
    {err && <Alert type="error" title={t(err.message)} showIcon />}
    {pending && <Alert type="warning" title={t('resellers.pending')} showIcon closable />}
    <Input.Search value={search} onChange={(e) => { setSearch(e.target.value); setPage(1); }} placeholder={t('resellers.search')} allowClear style={{ maxWidth: 360 }} />
    <Table className="p-reseller-clients-table" size="small" tableLayout="fixed" rowKey="id" loading={query.isPending} dataSource={query.data?.items} scroll={{ x: 965 }} pagination={{ current: page, pageSize: 50, total: query.data?.total, showSizeChanger: false, onChange: setPage }} columns={[
      { title: t('pages.clients.client'), key: 'client', width: 250, render: (_, r) => {
        const ended = r.expiryTime > 0 && r.expiryTime <= Date.now() || r.totalGB > 0 && r.trafficUsed >= r.totalGB;
        const near = r.expiryTime > 0 && r.expiryTime < Date.now() + 14 * 86_400_000 || r.totalGB > 0 && r.trafficUsed >= r.totalGB * 0.9;
        const key = ended ? 'ended' : !r.enable ? 'disabled' : near ? 'expiring' : 'active';
        const label = t(ended ? 'depleted' : !r.enable ? 'disabled' : near ? 'depletingSoon' : 'subscription.active');
        const color = ended ? 'var(--p-error)' : !r.enable ? 'var(--p-text-3)' : near ? 'var(--p-warning)' : 'var(--p-success)';
        return <div className="p-client-identity"><div className="p-client-name">
          <button type="button" className="p-text-action p-name" title={r.email} onClick={() => edit(r)}>{r.email}</button>
          <StatusPip status={{ key, label, color }} online={online(r)} />
          {me?.permissions.suspend && <Switch size="small" checked={r.enable} loading={mutation.isPending} disabled={r.expiryTime > 0 && r.expiryTime <= Date.now()} aria-label={`${t('pages.clients.enabled')}: ${r.email}`} onChange={(enable) => mutation.mutate({ path: `/reseller/clients/${r.id}/update`, body: { email: r.email, totalGB: r.totalGB, expiryTime: r.expiryTime, limitIp: r.limitIp, comment: r.comment, inboundIds: r.inboundIds || [], enable } })} />}
        </div><div className="p-client-context">
          {r.comment && <span className="p-desc" title={r.comment}>{r.comment}</span>}
          <span className="p-client-inbounds"><ClientInboundChips ids={(r.inboundIds || []).filter((id) => !!granted[id])} inboundsById={granted} protocolColors={{}} chipLimit={2} /></span>
        </div></div>;
      } },
      { title: t('telemetry.plan'), key: 'plan', width: 100, render: (_, r) => <div className="p-client-plan" title={r.plan?.name}><ClientPlanCell client={{ email: r.email, plan: r.plan }} /></div> },
      { title: t('pages.clients.traffic'), key: 'usage', width: 145, render: (_, r) => <ClientTrafficCell compact usedTotal={r.trafficUsed} total={r.totalGB} enabled={r.enable} /> },
      { title: t('telemetry.trend14d'), key: 'history', width: 100, render: (_, r) => <ClientTrendCell compact client={{ email: r.email, history: r.history }} /> },
      { title: t('lastOnline'), key: 'activity', width: 110, align: 'end', render: (_, r) => <div className="p-client-presence"><span>{t(online(r) ? 'resellers.online' : 'offline')}</span><div className="p-sub" title={r.lastOnline ? IntlUtil.formatDate(r.lastOnline, datepicker) : undefined}>{r.lastOnline ? relative(r.lastOnline) : t('telemetry.noReadings')}</div></div> },
      { title: t('resellers.expiry'), key: 'expiry', width: 110, align: 'end', render: (_, r) => <div className="p-client-expiry" title={expiry(r)}><span>{r.expiryTime ? relative(r.expiryTime) : '∞'}</span><div className="p-sub">{expiry(r)}</div></div> },
      { title: t('resellers.actions'), key: 'actions', width: 150, align: 'end', render: (_, r) => <div className="p-reseller-row-actions">
        <Tooltip title={t('edit')}><Button size="small" type="text" icon={<Icon name="edit" size={15} />} aria-label={`${t('edit')}: ${r.email}`} onClick={() => edit(r)} /></Tooltip>
        <Tooltip title={t('resellers.links')}><Button size="small" type="text" icon={<Icon name="link" size={15} />} aria-label={`${t('resellers.links')}: ${r.email}`} loading={linkMutation.isPending} onClick={() => linkMutation.mutate(r.id)} /></Tooltip>
        {me?.permissions.reset && <Popconfirm title={t('resellers.resetConfirm')} onConfirm={() => mutation.mutateAsync({ path: `/reseller/clients/${r.id}/reset` })}><Button size="small" type="text" icon={<Icon name="refresh" size={15} />} title={t('resellers.reset')} aria-label={`${t('resellers.reset')}: ${r.email}`} disabled={!r.enable && (r.totalGB === 0 || r.trafficUsed < r.totalGB)} /></Popconfirm>}
        <Popconfirm title={t('resellers.deleteConfirm')} onConfirm={() => mutation.mutateAsync({ path: `/reseller/clients/${r.id}/delete` })}><Button size="small" type="text" danger icon={<Icon name="trash" size={15} />} title={t('delete')} aria-label={`${t('delete')}: ${r.email}`} /></Popconfirm>
      </div> },
    ]} />
    <Modal open={editing !== false} title={t(editing ? 'resellers.editClient' : 'resellers.newClient')} onCancel={() => setEditing(false)} confirmLoading={mutation.isPending} onOk={() => void form.handleSubmit((v) => mutation.mutate({ path: editing ? `/reseller/clients/${editing.id}/update` : '/reseller/clients', body: { ...v, totalGB: Math.round(v.trafficGB * GiB) } }))()} width={650}>
      {mutation.error && <Alert type="error" title={t(mutation.error.message)} />}
      <FormProvider {...form}><Form layout="vertical" component="div">
        {!editing && me?.permissions.plans && <FormField name="planId" label={t('resellers.plan')}><Select options={[{ value: 0, label: t('resellers.noPlan') }, ...(plans.data || []).filter((p) => !p.archived).map((p) => ({ value: p.id, label: p.name }))]} /></FormField>}
        <FormField name="email" label={t('resellers.clientName')} required><Input disabled={!!editing} /></FormField>
        <div className="p-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
          <FormField name="trafficGB" label={`${t('resellers.traffic')} · GiB`}><InputNumber min={0} disabled={!!editing && !me?.permissions.topUp || selectedPlan?.quotaBytes != null} /></FormField>
          <FormField name="limitIp" label={t('resellers.ipLimit')}><InputNumber min={0} precision={0} disabled={selectedPlan?.limitIp != null} /></FormField>
        </div>
        <FormField name="expiryTime" label={t('resellers.expiry')} transform={{ input: (v) => v ? dayjs(Number(v)) : null, output: (v) => dayjs.isDayjs(v) ? v.valueOf() : 0 }}><DateTimePicker value={null} onChange={() => undefined} showTime disabled={!!editing && !me?.permissions.extend || selectedPlan?.durationDays != null} /></FormField>
        <FormField name="inboundIds" label={t('resellers.grants')}><Select mode="multiple" loading={inbounds.isPending} disabled={!!editing && !!editing.inboundIds?.length || !!selectedPlan?.inboundIds?.length} options={(inbounds.data || []).filter((i) => i.enable).map((i) => ({ value: i.id, label: `${i.remark} · ${i.protocol}` }))} /></FormField>
        <FormField name="enable" label={t('resellers.active')} valueProp="checked"><Switch disabled={!!editing && !me?.permissions.suspend} /></FormField>
        <FormField name="comment" label={t('resellers.comment')}><Input.TextArea rows={2} /></FormField>
        <Alert type="info" title={t('resellers.clientBudgetHelp')} />
      </Form></FormProvider>
    </Modal>
    <Modal open={!!links} title={t('resellers.links')} onCancel={() => setLinks(undefined)} footer={null}>
      {links?.length ? links.map((link) => <Typography.Paragraph key={link} copyable={{ text: link }} style={{ overflowWrap: 'anywhere' }}><bdi>{link}</bdi></Typography.Paragraph>) : t('resellers.noLinks')}
    </Modal>
  </div>;
}
