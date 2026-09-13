import { useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, InputNumber, Modal, Select, Space, Tag } from 'antd';
import { FormProvider, useWatch } from 'react-hook-form';
import { GroupFormSchema, type GroupFormValues } from '@/schemas/forms/group';
import { useZodForm, FormField } from '@/components/form/rhf';
import { PanelCard, Meter } from '@/components/ui/panel';
import { HttpUtil, SizeFormatter } from '@/utils';
import type { GroupSummary, InboundOption } from '@/schemas/client';
import './groups-parity.css';
import { inboundTone } from '../policies/inbound-tone';

function Section({ number, title, hint, children }: { number: number; title: string; hint: string; children: ReactNode }) {
  return <section className="groups-form-section"><div className="groups-form-heading"><span>{number}</span><div><strong>{title}</strong><div className="p-sub">{hint}</div></div></div>{children}</section>;
}

export default function GroupFormModal({ group, groups, inbounds, onClose, onSaved }: {
  group: GroupSummary | null; groups: GroupSummary[]; inbounds: InboundOption[]; onClose: () => void; onSaved: () => void;
}) {
  const { t } = useTranslation();
  const methods = useZodForm(GroupFormSchema, { defaultValues: { name: group?.name ?? '', quotaGiB: (group?.quotaBytes ?? 0) / 1073741824, inboundIds: group?.sharedInboundIds ?? [] } });
  const draft = useWatch({ control: methods.control });
  const persistedName = useRef(group?.name ?? null);
  const savingRef = useRef(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [partial, setPartial] = useState(false);
  const dirtyFields = methods.formState.dirtyFields;
  const selected = draft.inboundIds ?? [];
  const options = [...inbounds.map((ib) => ({ value: ib.id, label: `${ib.remark || ib.tag || ib.id} · ${ib.protocol ?? ''}` })),
    ...selected.filter((id) => !inbounds.some((ib) => ib.id === id)).map((id) => ({ value: id, label: `#${id}` }))];

  async function post(action: string, payload: object) {
    const msg = await HttpUtil.post(`/panel/api/clients/groups/${action}`, payload, { headers: { 'Content-Type': 'application/json' } });
    if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
  }

  async function save(values: GroupFormValues) {
    if (savingRef.current) return;
    if (groups.some((row) => row.name.toLowerCase() === values.name.toLowerCase() && row.name !== persistedName.current)) {
      methods.setError('name', { message: t('pages.groups.renameCollision', { name: values.name }) });
      return;
    }
    savingRef.current = true;
    setSaving(true); setError('');
    try {
      if (persistedName.current === null) {
        await post('create', { name: values.name });
        persistedName.current = values.name;
        setPartial(true); onSaved();
      } else if (persistedName.current !== values.name) {
        await post('rename', { oldName: persistedName.current, newName: values.name });
        persistedName.current = values.name;
        setPartial(true); onSaved();
      }
      if (dirtyFields.quotaGiB || dirtyFields.inboundIds) {
        await post('quota', { name: persistedName.current, quotaBytes: Math.round(values.quotaGiB * 1073741824),
          ...(dirtyFields.inboundIds ? { inboundIds: values.inboundIds } : {}) });
        onSaved();
      }
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('somethingWentWrong'));
    } finally { savingRef.current = false; setSaving(false); }
  }

  return <Modal open className="groups-parity-modal" width={820} mask={{ closable: false }}
    title={<div><div className="groups-modal-title">{group ? t('groupParity.edit', { defaultValue: 'Edit group' }) : t('pages.groups.addGroup')}</div><div className="p-sub">{t('pages.groups.subtitle')}</div></div>}
    onCancel={() => { if (!saving) onClose(); }} styles={{ body: { maxHeight: 'calc(100vh - 220px)', overflowY: 'auto' } }}
    footer={<Space size={8}><Button disabled={saving} onClick={onClose}>{t('cancel')}</Button><Button type="primary" loading={saving} onClick={methods.handleSubmit(save)}>{group ? t('save') : t('create')}</Button></Space>}>
    <FormProvider {...methods}><div className="groups-form-layout"><Form layout="vertical" onFinish={methods.handleSubmit(save)}>
      <Section number={1} title={t('groupParity.identity', { defaultValue: 'Name it' })} hint={t('groupParity.nameHint', { defaultValue: 'A shared name for this set of clients.' })}>
        <FormField name="name" required><Input size="large" aria-label={t('pages.groups.name')} placeholder={t('pages.clients.groupPlaceholder')} autoFocus /></FormField>
      </Section>
      <Section number={2} title={t('groupParity.pool', { defaultValue: 'Pooled quota' })} hint={t('groupParity.quotaHint', { defaultValue: 'One traffic allowance shared by every member.' })}>
        <FormField name="quotaGiB" label={t('telemetry.quotaGiB')}><InputNumber size="large" min={0} suffix="GiB" style={{ width: 180 }} /></FormField>
        <Space size={5} wrap>{[0, 50, 100, 500, 1000].map((value) => <Button key={value} size="small" onClick={() => methods.setValue('quotaGiB', value, { shouldDirty: true })}>{value ? value : '∞'}</Button>)}</Space>
        <p className="p-sub groups-form-note">{t('telemetry.groupQuotaHelp')}</p>
      </Section>
      <Section number={3} title={t('telemetry.sharedInbounds')} hint={t('groupParity.inboundsHint', { defaultValue: 'Members inherit these shared inbounds.' })}>
        <FormField name="inboundIds" label={t('telemetry.sharedInbounds')} extra={t('telemetry.sharedInboundsHelp')}>
          <Select mode="multiple" allowClear options={options} optionFilterProp="label" />
        </FormField>
        <Space size={5}><Button size="small" onClick={() => methods.setValue('inboundIds', [...new Set([...selected, ...inbounds.map((ib) => ib.id)])], { shouldDirty: true })}>{t('plans.selectAll')}</Button>
          <Button size="small" disabled={!selected.length} onClick={() => methods.setValue('inboundIds', [], { shouldDirty: true })}>{t('plans.clear')}</Button></Space>
      </Section>
      {error && <Alert className="groups-save-error" type="error" showIcon title={error} description={partial ? t('groupParity.partialSave', { defaultValue: 'The group name was saved. Save again to apply the remaining settings.' }) : undefined} />}
    </Form><aside className="groups-preview"><div className="p-kicker">{t('planParity.livePreview', { defaultValue: 'Live preview' })}</div><PanelCard title={draft.name || t('pages.groups.addGroup')} icon="groups" pad={0}>
      <div className="groups-preview-usage"><span className="p-stat">{SizeFormatter.sizeFormat(group?.trafficUsed ?? 0)}</span><span className="p-stat-u"> / {draft.quotaGiB ? SizeFormatter.sizeFormat(draft.quotaGiB * 1073741824) : '∞'}</span>
        <Meter pct={draft.quotaGiB ? Math.min(100, (group?.trafficUsed ?? 0) / (draft.quotaGiB * 1073741824) * 100) : 100} tone="var(--p-primary)" height={5} /></div>
      <div className="groups-preview-inbounds"><Space size={4} wrap>{selected.map((id) => <Tag key={id} color={inboundTone(inbounds.find((ib) => ib.id === id)?.protocol)}>{inbounds.find((ib) => ib.id === id)?.remark || inbounds.find((ib) => ib.id === id)?.tag || `#${id}`}</Tag>)}</Space>{!selected.length && <span className="p-sub">{t('noData')}</span>}</div>
      <div className="groups-preview-footer"><div className="p-kv"><span className="p-kv-k">{t('pages.groups.clientCount')}</span><span className="p-kv-v">{group?.clientCount ?? 0}</span></div></div>
    </PanelCard></aside></div></FormProvider>
  </Modal>;
}
