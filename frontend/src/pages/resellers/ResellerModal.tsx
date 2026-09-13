import { useEffect } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FormProvider, useWatch } from 'react-hook-form';
import { Alert, Button, Checkbox, Form, Input, InputNumber, Modal, Segmented, Select, Space, Switch, Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import dayjs from 'dayjs';
import { FormField, useZodForm } from '@/components/form/rhf';
import DateTimePicker from '@/components/form/DateTimePicker';
import { Kicker, PanelCard } from '@/components/ui/panel';
import { GiB, resellerGet, resellerKeys, resellerPost } from './api';
import { ResellerFormSchema, type ResellerForm, type ResellerView } from './schema';

const InboundOptionsSchema = z.array(z.object({ id: z.number(), remark: z.string(), protocol: z.string() }));
const defaults = (): ResellerForm => ({ name: '', username: '', email: '', password: '', trafficGB: 500, clientCap: 50, expiresAt: Date.now() + 90 * 86_400_000, inboundIds: [], allowPlans: true, allowReset: true, allowExtend: false, allowSuspend: true, allowTopUp: true, notifyOnDepletion: true, note: '', onDepletion: 'disable', resetCycle: 'none' });

export default function ResellerModal({ open, reseller, onClose }: { open: boolean; reseller?: ResellerView; onClose: () => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const form = useZodForm(ResellerFormSchema, { defaultValues: defaults() });
  const draft = useWatch({ control: form.control });
  const inbounds = useQuery({ queryKey: [...resellerKeys, 'grant-options'], queryFn: () => resellerGet('/inbounds/list', InboundOptionsSchema), enabled: open });
  const mutation = useMutation({ mutationFn: (value: ResellerForm) => resellerPost(reseller ? `/resellers/${reseller.account.userId}/update` : '/resellers', { ...value, trafficLimit: Math.round(value.trafficGB * GiB) }), onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: resellerKeys }); onClose(); } });
  useEffect(() => {
    if (!open) return;
    form.reset(reseller ? { ...reseller.account, username: reseller.username, password: '', trafficGB: reseller.account.trafficLimit / GiB, note: reseller.account.note || '', inboundIds: reseller.inboundIds } : defaults());
  }, [open, reseller, form]);
  const submit = form.handleSubmit((v) => {
    if (!reseller && !v.password) { form.setError('password', { message: 'resellers.passwordLength' }); return; }
    mutation.mutate(v);
  });
  return <Modal open={open} onCancel={onClose} title={t(reseller ? 'resellers.edit' : 'resellers.add')} width={920} destroyOnHidden onOk={() => void submit()} confirmLoading={mutation.isPending} okText={t('save')} cancelText={t('cancel')}>
    <p className="p-sub">{t('resellers.description')}</p>
    {mutation.error && <Alert type="error" title={t(mutation.error.message)} showIcon />}
    <FormProvider {...form}><Form layout="vertical" component="div">
      <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(250px,1fr))' }}>
        <div>
          <Kicker>{t('resellers.identity')}</Kicker>
          <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(180px,1fr))' }}>
            <FormField name="name" label={t('resellers.name')} required><Input /></FormField>
            <FormField name="username" label={t('resellers.handle')} required><Input autoComplete="off" prefix="@" /></FormField>
            <FormField name="email" label={t('resellers.email')}><Input type="email" /></FormField>
            <FormField name="password" label={t('resellers.password')} extra={t(reseller ? 'resellers.passwordUnchanged' : 'resellers.passwordRequired')}><Input.Password autoComplete="new-password" /></FormField>
          </div>
          <Kicker>{t('resellers.budget')}</Kicker>
          <FormField name="trafficGB" label={`${t('resellers.traffic')} · GiB`} extra={t('resellers.zeroUnlimited')}><InputNumber min={0} style={{ width: '100%' }} /></FormField>
          <Space wrap style={{ marginBottom: 12 }}>{[100, 500, 1024, 2048].map((v) => <Button key={v} size="small" onClick={() => form.setValue('trafficGB', v)}>{v} GiB</Button>)}</Space>
          <FormField name="clientCap" label={t('resellers.clientCap')} extra={t('resellers.zeroUnlimited')}><InputNumber min={0} precision={0} /></FormField>
          <FormField name="expiresAt" label={t('resellers.expiry')} transform={{ input: (v) => v ? dayjs(Number(v)) : null, output: (v) => dayjs.isDayjs(v) ? v.valueOf() : 0 }} extra={t('resellers.clearUnlimited')}><DateTimePicker value={null} onChange={() => undefined} showTime /></FormField>
          <Space wrap style={{ marginBottom: 16 }}>{[30, 90, 180, 365].map((v) => <Button key={v} size="small" onClick={() => form.setValue('expiresAt', Date.now() + v * 86_400_000)}>{t('resellers.days', { days: v })}</Button>)}</Space>
          <Kicker>{t('resellers.rules')}</Kicker>
          <FormField name="onDepletion" label={t('resellers.onDepletion')} extra={t('resellers.onDepletionHelp')}><Segmented block options={[{ value: 'disable', label: t('resellers.policy.disable') }, { value: 'block', label: t('resellers.policy.block') }]} /></FormField>
          <FormField name="resetCycle" label={t('resellers.resetCycle')} extra={t('resellers.resetCycleHelp')}><Select options={(['none', 'weekly', 'monthly'] as const).map((v) => ({ value: v, label: t(`resellers.cycle.${v}`) }))} /></FormField>
          <Kicker>{t('resellers.grants')}</Kicker>
          {inbounds.error && <Alert type="error" title={t('resellers.loadError')} action={<Button onClick={() => void inbounds.refetch()}>{t('resellers.retry')}</Button>} />}
          <Space style={{ marginBottom: 8 }}><Button size="small" onClick={() => form.setValue('inboundIds', inbounds.data?.map((i) => i.id) || [])}>{t('resellers.grantAll')}</Button><Button size="small" onClick={() => form.setValue('inboundIds', [])}>{t('resellers.clear')}</Button></Space>
          <FormField name="inboundIds"><Checkbox.Group style={{ display: 'grid', gap: 10 }} options={inbounds.data?.map((i) => ({ value: i.id, label: <>{i.remark} <Tag>{i.protocol}</Tag></> }))} /></FormField>
          {([
            ['allowPlans', 'resellers.allowPlans'], ['allowReset', 'resellers.allowReset'],
            ['allowExtend', 'resellers.allowExtend'], ['allowSuspend', 'resellers.allowSuspend'],
            ['allowTopUp', 'resellers.allowTopUp'], ['notifyOnDepletion', 'resellers.notifyOnDepletion'],
          ] as const).map(([key, label]) => <FormField key={key} name={key} label={t(label)} valueProp="checked"><Switch /></FormField>)}
          <FormField name="note" label={t('resellers.note')} extra={t('resellers.notePrivate')}><Input.TextArea rows={2} /></FormField>
        </div>
        <aside style={{ alignSelf: 'start', position: 'sticky', top: 16 }}>
          <PanelCard kicker={t('resellers.preview')}>
            <div className="p-avatar">{draft.name?.slice(0, 2).toUpperCase() || '—'}</div><h3>{draft.name || t('resellers.add')}</h3><div className="p-sub">@{draft.username}</div>
            <div className="p-grid" style={{ gridTemplateColumns: '1fr 1fr', marginTop: 16 }}>
              <div><Kicker>{t('resellers.traffic')}</Kicker><strong>{draft.trafficGB ? `${draft.trafficGB} GiB` : '∞'}</strong></div>
              <div><Kicker>{t('resellers.clients')}</Kicker><strong>{draft.clientCap || '∞'}</strong></div>
              <div><Kicker>{t('resellers.expiry')}</Kicker><strong>{draft.expiresAt ? new Date(draft.expiresAt).toLocaleDateString() : '∞'}</strong></div>
              <div><Kicker>{t('resellers.grants')}</Kicker><strong>{draft.inboundIds?.length || 0}</strong></div>
              <div><Kicker>{t('resellers.onDepletion')}</Kicker><strong>{t(`resellers.policy.${draft.onDepletion || 'disable'}`)}</strong></div>
              <div><Kicker>{t('resellers.resetCycle')}</Kicker><strong>{t(`resellers.cycle.${draft.resetCycle || 'none'}`)}</strong></div>
            </div>
            <p className="p-sub" style={{ whiteSpace: 'normal' }}>{t('resellers.budgetExplanation')}</p>
          </PanelCard>
        </aside>
      </div>
    </Form></FormProvider>
  </Modal>;
}
