import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Checkbox, Col, Collapse, Form, Input, InputNumber, Modal, Row, Segmented, Select, Space, Switch, Tag, Typography } from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { FormProvider, useFieldArray, useFormState, useWatch } from 'react-hook-form';

import { FormField, useZodForm } from '@/components/form/rhf';
import { usePolicyMutations } from '@/api/queries/usePolicyMutations';
import { describeTier, formatRate } from '@/lib/policies/labels';
import { PanelCard } from '@/components/ui/panel';
import { Icon } from '@/components/ui/Icon';
import {
  PolicyFormSchema,
  RATE_UNITS,
  emptyTierRow,
  tierRowsToWire,
  planFormValues,
  ONE_GB_BYTES,
  type TierRow,
  type PolicyFormValues,
  type PolicyRecord,
  type PolicyInput,
} from '@/schemas/api/policy';
import type { InboundOption } from '@/schemas/client';
import { SizeFormatter } from '@/utils';
import { KIND_GROUPS } from '@/lib/cores/kind-labels';
import './plans-parity.css';
import { inboundTone } from './inbound-tone';

interface PolicyFormModalProps {
  open: boolean;
  policy: PolicyInput | null;
  onClose: () => void;
  plans?: PolicyRecord[];
  inbounds?: InboundOption[];
  reseller?: boolean;
}

const UNIT_OPTIONS = RATE_UNITS.map((u) => ({ label: u.label, value: u.key }));

function FormSection({ number, title, description, children }: { number: number; title: string; description?: string; children: ReactNode }) {
  return <section className="plans-form-section"><div className="plans-form-section-heading">
    <span className="plans-form-section-number">{number}</span><div><div className="plans-section-title">{title}</div>
      {description && <div className="p-sub">{description}</div>}</div>
  </div>{children}</section>;
}

function PlanInboundPicker({ inbounds, value = [], onChange }: { inbounds: InboundOption[]; value?: number[]; onChange?: (ids: number[]) => void }) {
  const { t } = useTranslation();
  const grouped = new Map<string, InboundOption[]>();
  for (const inbound of inbounds) {
    const group = KIND_GROUPS.find((item) => item.kinds.some((kind) => kind === inbound.protocol));
    const label = group ? t(group.titleKey) : inbound.protocol || '—';
    grouped.set(label, [...(grouped.get(label) ?? []), inbound]);
  }
  return <div className="plans-inbound-picker">
    {[...grouped].map(([label, items]) => <PanelCard key={label} title={label} icon="inbounds" pad={0} extra={<Button type="text" size="small" onClick={() => onChange?.([...new Set([...value, ...items.map((item) => item.id)])])}>{t('plans.selectAll')}</Button>}>
      {items.map((item) => <div key={item.id} className={`plans-pickrow${value.includes(item.id) ? ' is-on' : ''}`}>
        <Checkbox checked={value.includes(item.id)} onChange={(e) => onChange?.(e.target.checked ? [...value, item.id] : value.filter((id) => id !== item.id))}>
          <Tag color={inboundTone(item.protocol)}>{item.remark || item.tag || `#${item.id}`}</Tag><span className="p-sub" dir="ltr">{item.protocol}{item.port ? ` · ${item.port}` : ''}</span>
        </Checkbox>
      </div>)}
    </PanelCard>)}
    {value.filter((id) => !inbounds.some((item) => item.id === id)).map((id) => <Checkbox key={id} checked onChange={() => onChange?.(value.filter((item) => item !== id))}>#{id}</Checkbox>)}
  </div>;
}

function defaultValues(): PolicyFormValues {
  return planFormValues();
}

/* Unlimited is picked, never typed: a rate field left at zero and a rate field
   meaning "no cap" are the same keystroke otherwise. */
function DirectionFields({ index, dir, label }: { index: number; dir: 'up' | 'down'; label: string }) {
  const { t } = useTranslation();
  const limited = useWatch({ name: `tiers.${index}.${dir}Limited` }) as boolean | undefined;
  /* The rate field is noStyle so the unit picker sits beside it, and a noStyle
     Form.Item renders no help text — so the message is placed by hand. */
  const { errors } = useFormState<PolicyFormValues>({ name: `tiers.${index}.${dir}Value` });
  const error = errors.tiers?.[index]?.[`${dir}Value`]?.message;

  return (
    <Space direction="vertical" size={4} style={{ width: '100%' }}>
      <FormField
        name={`tiers.${index}.${dir}Limited`}
        label={label}
        transform={{
          input: (v) => (v ? 'limited' : 'unlimited'),
          output: (v) => v === 'limited',
        }}
      >
        <Segmented
          block
          options={[
            { label: t('pages.policies.unlimited'), value: 'unlimited' },
            { label: t('pages.policies.capped'), value: 'limited' },
          ]}
        />
      </FormField>
      {limited && (
        <>
          <Space.Compact style={{ display: 'flex' }}>
            <FormField
              name={`tiers.${index}.${dir}Value`}
              noStyle
              transform={{ input: (v) => (v ? v : undefined), output: (v) => Number(v) || 0 }}
            >
              <InputNumber min={0} step={1} style={{ flex: 1 }} placeholder={t('pages.policies.ratePlaceholder')} />
            </FormField>
            <FormField name={`tiers.${index}.${dir}Unit`} noStyle>
              <Select options={UNIT_OPTIONS} style={{ width: 96 }} />
            </FormField>
          </Space.Compact>
          {error && <Typography.Text type="danger">{t(error)}</Typography.Text>}
        </>
      )}
    </Space>
  );
}

export default function PolicyFormModal({ open, policy, onClose, plans = [], inbounds = [], reseller = false }: PolicyFormModalProps) {
  const { t } = useTranslation();
  const methods = useZodForm(PolicyFormSchema, { defaultValues: defaultValues() });
  const { fields, append, remove } = useFieldArray({ control: methods.control, name: 'tiers' });
  const [submitting, setSubmitting] = useState(false);
  const [advanced, setAdvanced] = useState<string[]>([]);
  const shaped = useRef<TierRow[] | null>(null);
  const submittingRef = useRef(false);
  const { add, update } = usePolicyMutations(reseller);
  const tiers = useWatch({ control: methods.control, name: 'tiers' });
  const draft = useWatch({ control: methods.control });

  useEffect(() => {
    if (!open) return;
    const values = planFormValues(policy);
    if (!policy && reseller) values.startOnFirstUse = false;
    methods.reset(values);
    shaped.current = null;
    setAdvanced([]);
  }, [open, policy, methods, reseller]);

  const preview = tierRowsToWire(tiers ?? []);
  const lowest = preview.length > 0 ? preview[0].fromBytes : 0;
  const baseIndex = (tiers ?? []).findIndex((tier) => tier.fromGB === 0);
  const base = tiers?.[baseIndex];
  const noShaping = !(tiers ?? []).some((tier) => tier.downLimited || tier.upLimited);
  const initial = preview.find((tier) => tier.fromBytes === 0);
  const validation = PolicyFormSchema.safeParse(draft);

  function changeBase(patch: Partial<TierRow>) {
    const current = methods.getValues('tiers');
    const index = current.findIndex((tier) => tier.fromGB === 0);
    if (index < 0) methods.setValue('tiers', [{ ...emptyTierRow(), ...patch }, ...current], { shouldDirty: true });
    else methods.setValue(`tiers.${index}`, { ...current[index], ...patch }, { shouldDirty: true });
  }

  function preset(down: number, up: number) {
    changeBase({ downLimited: true, downValue: down, downUnit: 'mbps', upLimited: true, upValue: up, upUnit: 'mbps' });
  }

  function toggleShaping(off: boolean) {
    if (off) {
      shaped.current = methods.getValues('tiers').map((tier) => ({ ...tier }));
      methods.setValue('tiers', shaped.current.map((tier) => ({ ...tier, downLimited: false, upLimited: false })), { shouldDirty: true });
    } else if (shaped.current) {
      methods.setValue('tiers', methods.getValues('tiers').map((tier) => {
        const previous = shaped.current?.find((row) => row.fromGB === tier.fromGB);
        return previous ? { ...tier, upLimited: previous.upLimited, downLimited: previous.downLimited } : tier;
      }), { shouldDirty: true });
      shaped.current = null;
    } else preset(20, 5);
  }

  const submit = methods.handleSubmit(onFinish, (errors) => {
    if (errors.tiers) setAdvanced((keys) => [...new Set([...keys, 'rates'])]);
    if (errors.onDepletion || errors.autoRenewDays || errors.startOnFirstUse) setAdvanced((keys) => [...new Set([...keys, 'advanced'])]);
  });

  async function onFinish(form: PolicyFormValues) {
    if (submittingRef.current) return;
    submittingRef.current = true;
    setSubmitting(true);
    try {
      const payload = { ...form, name: form.name.trim(), tiers: tierRowsToWire(form.tiers) };
      const msg = policy ? await update(policy.id, payload) : await add(payload);
      if (msg?.success) onClose();
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  }

  return (
    <Modal
      open={open}
      className="plans-parity-modal"
      title={<div><div className="plans-modal-title">{policy ? t('pages.policies.edit') : t('pages.policies.add')}</div><div className="p-sub">{t('planParity.formIntro', { defaultValue: 'Four things make a plan: inbounds, duration, traffic and speed. Clients attached to it inherit all four.' })}</div></div>}
      confirmLoading={submitting}
      okText={t('save')}
      cancelText={t('cancel')}
      mask={{ closable: false }}
      width={920}
      styles={{ body: { maxHeight: 'calc(100vh - 220px)', overflowY: 'auto' } }}
      onOk={submit}
      onCancel={() => { if (!submitting) onClose(); }}
      footer={<div className="plans-form-footer"><span className="p-sub">{validation.success ? t('planParity.ready', { defaultValue: 'Ready to save' }) : t(validation.error.issues[0].message)}</span><Space size={8}>
        <Button disabled={submitting} onClick={onClose}>{t('cancel')}</Button>
        <Button type="primary" loading={submitting} onClick={submit}>{policy ? t('save') : t('pages.policies.add')}</Button>
      </Space></div>}
    >
      <FormProvider {...methods}>
        <div className="plans-form-layout">
        <div>
        <Form layout="vertical">
          <FormSection number={1} title={t('planParity.nameSection', { defaultValue: 'Name it' })} description={t('planParity.nameHint', { defaultValue: 'Shown in the Clients table and in the plan picker.' })}>
          <FormField name="name" required>
            <Input size="large" aria-label={t('pages.policies.name')} placeholder={t('pages.policies.namePlaceholder')} />
          </FormField>
          <FormField name="description"><Input aria-label={t('plans.description')} placeholder={t('plans.description')} maxLength={2000} /></FormField>
          <Space wrap size={6}><Typography.Text type="secondary">{t('plans.copyFrom')}</Typography.Text>
            {plans.filter((p) => p.id !== policy?.id && !p.archived).map((p) => <Button size="small" key={p.id} onClick={() => {
              shaped.current = null;
              methods.reset({ ...planFormValues(p), id: policy?.id ?? 0, name: methods.getValues('name'), description: methods.getValues('description') });
            }}>{p.name}</Button>)}
          </Space>
          </FormSection>
          <FormSection number={2} title={t('planParity.quotaSection', { defaultValue: 'Traffic and duration' })} description={t('planParity.quotaHint', { defaultValue: 'What every client on this plan gets.' })}>
          <Row gutter={12}>
            <Col xs={24} sm={12}><FormField label={t('plans.quota')} name="quotaBytes" transform={{ input: (v) => v === null ? null : Number(v) / ONE_GB_BYTES, output: (v) => v === null ? null : Math.round(Number(v) * ONE_GB_BYTES) }}>
              <InputNumber size="large" min={0} max={8388607} suffix="GiB" placeholder={t('design.inherit')} style={{ width: '100%' }} />
            </FormField><Space wrap size={4}>{[0, 10, 50, 100, 500, 1000].map((n) => <Button size="small" key={n} onClick={() => methods.setValue('quotaBytes', n * ONE_GB_BYTES, { shouldDirty: true })}>{n === 0 ? '∞' : n}</Button>)}</Space></Col>
            <Col xs={24} sm={12}><FormField label={t('plans.duration')} name="durationDays"><InputNumber size="large" min={0} max={36500} placeholder={t('design.inherit')} style={{ width: '100%' }} /></FormField>
              <Space wrap size={4}>{[0, 7, 30, 90, 365].map((n) => <Button size="small" key={n} onClick={() => methods.setValue('durationDays', n, { shouldDirty: true })}>{n === 0 ? '∞' : n}</Button>)}</Space>
            </Col>
          </Row>
          <p className="p-sub plans-form-hint">{t('plans.nullableHint')}</p>
          </FormSection>
          <FormSection number={3} title={t('planParity.speedSection', { defaultValue: 'Speed and devices' })} description={t('planParity.speedHint', { defaultValue: 'Leave shaping off for unlimited throughput.' })}>

          <div className="plans-speed-controls">
            {(['down', 'up'] as const).map((direction) => <div className="plans-speed-input" key={direction}>
              <label className="p-kicker" htmlFor={`plan-rate-${direction}`}>{t(`pages.policies.${direction === 'down' ? 'download' : 'upload'}`)}</label>
              <Space.Compact>
                <InputNumber id={`plan-rate-${direction}`} size="large" min={0} disabled={noShaping} value={base?.[`${direction}Limited`] ? base[`${direction}Value`] || null : null}
                  placeholder="∞" onChange={(value) => changeBase({ [`${direction}Value`]: value ?? 0, [`${direction}Limited`]: true })} />
                <Select size="large" aria-label={t(`pages.policies.${direction === 'down' ? 'download' : 'upload'}`)} disabled={noShaping} value={base?.[`${direction}Unit`] ?? 'mbps'} options={UNIT_OPTIONS}
                  onChange={(unit) => changeBase({ [`${direction}Unit`]: unit })} />
              </Space.Compact>
            </div>)}
            <label className="plans-inline-switch"><Switch size="small" checked={noShaping} onChange={toggleShaping} />{t('planParity.noShaping', { defaultValue: 'No shaping' })}</label>
          </div>
          <Space size={5} wrap className="plans-speed-presets">{[[2, 1], [20, 5], [100, 25], [250, 60]].map(([down, up]) => <Button key={down} size="small" disabled={noShaping} onClick={() => preset(down, up)}>{down}/{up}</Button>)}</Space>
          <FormField className="plans-devices" label={t('plans.devices')} name="limitIp"><InputNumber min={0} max={1000000} placeholder={t('design.inherit')} style={{ width: 120 }} /></FormField>
          {!preview.length && <p className="p-sub">{t('pages.policies.noTiersHint')}</p>}
          </FormSection>
          <FormSection number={4} title={t('plansFidelity.includedInbounds')} description={t('plans.inboundsHint')}>
          <Space className="plans-inbound-actions">
            <Button size="small" onClick={() => methods.setValue('inboundIds', inbounds.map((i) => i.id), { shouldDirty: true })}>{t('plans.selectAll')}</Button>
            <Button size="small" onClick={() => methods.setValue('inboundIds', [], { shouldDirty: true })}>{t('plans.clear')}</Button>
          </Space>
          <FormField name="inboundIds">
            <PlanInboundPicker inbounds={inbounds} />
          </FormField>
          {!inbounds.length && <Alert type="info" title={t('plans.noInbounds')} />}
          </FormSection>
          <FormSection number={5} title={t('planParity.renewalSection', { defaultValue: 'Renewal and edge cases' })} description={t('planParity.renewalHint', { defaultValue: 'Defaults suit most plans — open only if you need them.' })}>
          <Collapse className="plans-advanced" activeKey={advanced} onChange={(keys) => setAdvanced(typeof keys === 'string' ? [keys] : keys)} items={[{ key: 'advanced', label: t('planParity.advanced', { defaultValue: 'Advanced options' }), children: <>
            {reseller && <Alert type="info" title={t('plans.resellerWindow')} style={{ marginBottom: 12 }} />}
            <FormField label={t('plans.firstUse')} name="startOnFirstUse" valueProp="checked"><Switch disabled={reseller} /></FormField>
            <FormField label={t('plans.autoRenew')} name="autoRenewDays"><InputNumber min={0} max={36500} disabled={reseller} /></FormField>
            <Typography.Paragraph type="secondary">{t('plans.autoRenewHint')}</Typography.Paragraph>
            <FormField label={t('plans.resetCycle')} name="resetCycle"><Segmented options={[{ value: 'none', label: t('plans.never') }, { value: 'weekly', label: t('plans.weekly') }, { value: 'monthly', label: t('plans.monthly') }]} /></FormField>
            <FormField label={t('plans.onDepletion')} name="onDepletion"><Select options={[{ value: 'disable', label: t('plans.disable') }, { value: 'throttle', label: t('plans.throttle') }, { value: 'notify', label: t('plans.notify') }]} /></FormField>
            <Alert type="info" title={t('plans.continuationHint')} style={{ marginBottom: 12 }} />
            <Form.Item label={t('plans.burst')}><Switch checked={false} disabled /><Typography.Paragraph type="secondary">{t('plans.burstUnsupported')}</Typography.Paragraph></Form.Item>
            <FormField label={t('plans.archived')} name="archived" valueProp="checked"><Switch /></FormField>
          </> }, { key: 'rates', forceRender: true, label: t('pages.policies.ladder'), children: <>          {fields.map((field, index) => (
            <Card
              key={field.id}
              size="small"
              className={`plans-tier${fields.length === 1 && tiers?.[index]?.fromGB === 0 ? ' is-base' : ''}`}
              style={{ marginBottom: 12 }}
              title={t('pages.policies.tierNumber', { number: index + 1 })}
              extra={(
                <Button
                  size="small"
                  danger
                  icon={<DeleteOutlined />}
                  aria-label={t('pages.policies.removeTier')}
                  onClick={() => remove(index)}
                />
              )}
            >
              <Row gutter={16}>
                <Col xs={24} md={8}>
                  {/* An empty threshold is the first byte, and the placeholder says so rather than leaving a bare 0. */}
                  <FormField
                    name={`tiers.${index}.fromGB`}
                    label={t('pages.policies.threshold')}
                    tooltip={t('pages.policies.thresholdHint')}
                    transform={{ input: (v) => (v ? v : undefined), output: (v) => Number(v) || 0 }}
                  >
                    <InputNumber
                      min={0}
                      step={1}
                      addonAfter="GB"
                      style={{ width: '100%' }}
                      placeholder={t('pages.policies.fromStart')}
                    />
                  </FormField>
                </Col>
                <Col xs={24} md={8}>
                  <DirectionFields index={index} dir="down" label={t('pages.policies.download')} />
                </Col>
                <Col xs={24} md={8}>
                  <DirectionFields index={index} dir="up" label={t('pages.policies.upload')} />
                </Col>
              </Row>
            </Card>
          ))}

          <Button
            block
            icon={<PlusOutlined />}
            onClick={() => append(emptyTierRow(0))}
          >
            {t('pages.policies.addTier')}
          </Button>
          {preview.length > 0 && (
            <div style={{ marginTop: 16 }}>
              <Typography.Text strong>{t('pages.policies.preview')}</Typography.Text>
              <ul style={{ marginTop: 4, marginBottom: 0, paddingInlineStart: 20 }}>
                {lowest > 0 && (
                  <li><Typography.Text type="secondary">{t('pages.policies.belowFirstHint')}</Typography.Text></li>
                )}
                {preview.map((tier) => (
                  <li key={tier.fromBytes}>
                    <span dir="ltr">{describeTier(tier, t)}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
</> }]} />
          </FormSection>

        </Form>
        </div>
        <aside className="plans-preview">
          <div className="p-kicker">{t('planParity.livePreview', { defaultValue: 'Live preview' })}</div>
          <PanelCard pad={0}>
            <div className="plans-preview-heading"><div className="p-name">{draft.name || t('plans.untitled')}</div><div className="p-sub">{draft.description || t('planParity.noDescription', { defaultValue: 'No description yet' })}</div></div>
            <div className="plans-preview-specs">
            {[['plans.quota', draft.quotaBytes == null ? t('design.inherit') : draft.quotaBytes === 0 ? t('pages.policies.unlimited') : SizeFormatter.sizeFormat(draft.quotaBytes)],
              ['plans.duration', draft.durationDays == null ? t('design.inherit') : draft.durationDays === 0 ? t('pages.policies.unlimited') : t('design.days', { count: draft.durationDays })],
              ['plansFidelity.speed', initial?.downBps ? `${formatRate(initial.downBps, t)} ↓` : t('plansFidelity.unshaped')],
              ['plans.devices', draft.limitIp == null ? t('design.inherit') : draft.limitIp === 0 ? t('pages.policies.unlimited') : String(draft.limitIp)],
            ].map(([label, value]) => <div key={label}><div className="p-kicker">{t(label)}</div><div className="p-val">{value}</div></div>)}
            </div>
            <div className="plans-preview-inbounds"><div className="p-kicker"><Icon name="inbounds" size={13} /> {t('plansFidelity.includedInbounds')}</div>
            <Space wrap size={4}>{draft.inboundIds?.map((id) => <Tag key={id} color={inboundTone(inbounds.find((i) => i.id === id)?.protocol)}>{inbounds.find((i) => i.id === id)?.remark || inbounds.find((i) => i.id === id)?.tag || `#${id}`}</Tag>)}</Space>
            {!draft.inboundIds?.length && <span className="p-sub">{t('noData')}</span>}</div>
          </PanelCard>
          <p className="plans-preview-note">{draft.startOnFirstUse ? t('plans.firstUse') : t('plans.startsNow')}{!!draft.autoRenewDays && <> · {t('plans.autoRenew')}: {t('design.days', { count: draft.autoRenewDays })}</>}</p>
          {policy && <p className="plans-preview-note">{t('plans.reapplyHint')}</p>}
        </aside>
        </div>
      </FormProvider>
    </Modal>
  );
}
