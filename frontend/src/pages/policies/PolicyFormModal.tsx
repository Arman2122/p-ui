import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Checkbox, Col, Collapse, Divider, Form, Input, InputNumber, Modal, Row, Segmented, Select, Space, Switch, Tag, Typography } from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { FormProvider, useFieldArray, useFormState, useWatch } from 'react-hook-form';

import { FormField, useZodForm } from '@/components/form/rhf';
import { usePolicyMutations } from '@/api/queries/usePolicyMutations';
import { describeTier } from '@/lib/policies/labels';
import {
  PolicyFormSchema,
  RATE_UNITS,
  emptyTierRow,
  tierRowsToWire,
  planFormValues,
  ONE_GB_BYTES,
  type PolicyFormValues,
  type PolicyRecord,
  type PolicyInput,
} from '@/schemas/api/policy';
import type { InboundOption } from '@/schemas/client';
import { SizeFormatter } from '@/utils';
import { KIND_GROUPS } from '@/lib/cores/kind-labels';

interface PolicyFormModalProps {
  open: boolean;
  policy: PolicyInput | null;
  onClose: () => void;
  plans?: PolicyRecord[];
  inbounds?: InboundOption[];
  reseller?: boolean;
}

const UNIT_OPTIONS = RATE_UNITS.map((u) => ({ label: u.label, value: u.key }));

function PlanInboundPicker({ inbounds, value = [], onChange }: { inbounds: InboundOption[]; value?: number[]; onChange?: (ids: number[]) => void }) {
  const { t } = useTranslation();
  const grouped = new Map<string, InboundOption[]>();
  for (const inbound of inbounds) {
    const group = KIND_GROUPS.find((item) => item.kinds.some((kind) => kind === inbound.protocol));
    const label = group ? t(group.titleKey) : inbound.protocol || '—';
    grouped.set(label, [...(grouped.get(label) ?? []), inbound]);
  }
  return <Space orientation="vertical" style={{ width: '100%' }}>
    {[...grouped].map(([label, items]) => <Card key={label} size="small" title={label} extra={<Button type="text" size="small" onClick={() => onChange?.([...new Set([...value, ...items.map((item) => item.id)])])}>{t('plans.selectAll')}</Button>}>
      <Space orientation="vertical" style={{ width: '100%' }}>{items.map((item) => <Checkbox key={item.id} checked={value.includes(item.id)} onChange={(e) => onChange?.(e.target.checked ? [...value, item.id] : value.filter((id) => id !== item.id))}>
        <Space><Tag>{item.remark || item.tag || `#${item.id}`}</Tag><span dir="ltr">{item.protocol}{item.port ? ` · ${item.port}` : ''}</span></Space>
      </Checkbox>)}</Space>
    </Card>)}
  </Space>;
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
  const { add, update } = usePolicyMutations(reseller);
  const tiers = useWatch({ control: methods.control, name: 'tiers' });
  const draft = useWatch({ control: methods.control });

  useEffect(() => {
    if (!open) return;
    const values = planFormValues(policy);
    if (!policy && reseller) values.startOnFirstUse = false;
    methods.reset(values);
  }, [open, policy, methods, reseller]);

  const preview = tierRowsToWire(tiers ?? []);
  const lowest = preview.length > 0 ? preview[0].fromBytes : 0;

  async function onFinish(form: PolicyFormValues) {
    setSubmitting(true);
    try {
      const payload = { ...form, name: form.name.trim(), tiers: tierRowsToWire(form.tiers) };
      const msg = policy ? await update(policy.id, payload) : await add(payload);
      if (msg?.success) onClose();
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal
      open={open}
      title={policy ? t('pages.policies.edit') : t('pages.policies.add')}
      confirmLoading={submitting}
      okText={t('save')}
      cancelText={t('cancel')}
      mask={{ closable: false }}
      width="1040px"
      styles={{ body: { maxHeight: 'calc(100vh - 220px)', overflowY: 'auto' } }}
      onOk={methods.handleSubmit(onFinish)}
      onCancel={() => { if (!submitting) onClose(); }}
    >
      <FormProvider {...methods}>
        <Row gutter={24}>
        <Col xs={24} lg={16}>
        <Form layout="vertical">
          <Typography.Title level={5}>{t('plans.nameSection')}</Typography.Title>
          <FormField label={t('pages.policies.name')} name="name" required>
            <Input placeholder={t('pages.policies.namePlaceholder')} />
          </FormField>
          <FormField label={t('plans.description')} name="description"><Input.TextArea rows={2} maxLength={2000} /></FormField>
          <Space wrap size={6}><Typography.Text type="secondary">{t('plans.copyFrom')}</Typography.Text>
            {plans.filter((p) => p.id !== policy?.id && !p.archived).map((p) => <Button size="small" key={p.id} onClick={() => {
              methods.reset({ ...planFormValues(p), id: policy?.id ?? 0, name: methods.getValues('name'), description: methods.getValues('description') });
            }}>{p.name}</Button>)}
          </Space>
          <Divider />
          <Typography.Title level={5}>{t('plans.quotaSection')}</Typography.Title>
          <Alert type="info" showIcon title={t('plans.nullableHint')} style={{ marginBottom: 12 }} />
          <Row gutter={12}>
            <Col xs={24} sm={12}><FormField label={t('plans.quota')} name="quotaBytes" transform={{ input: (v) => v === null ? null : Number(v) / ONE_GB_BYTES, output: (v) => v === null ? null : Math.round(Number(v) * ONE_GB_BYTES) }}>
              <InputNumber min={0} max={8388607} suffix="GiB" placeholder={t('design.inherit')} style={{ width: '100%' }} />
            </FormField><Space wrap size={4}>{[0, 10, 50, 100, 500, 1000].map((n) => <Button size="small" key={n} onClick={() => methods.setValue('quotaBytes', n * ONE_GB_BYTES, { shouldDirty: true })}>{n === 0 ? '∞' : n}</Button>)}</Space></Col>
            <Col xs={24} sm={12}><FormField label={t('plans.duration')} name="durationDays"><InputNumber min={0} max={36500} placeholder={t('design.inherit')} style={{ width: '100%' }} /></FormField>
              <Space wrap size={4}>{[0, 7, 30, 90, 365].map((n) => <Button size="small" key={n} onClick={() => methods.setValue('durationDays', n, { shouldDirty: true })}>{n === 0 ? '∞' : n}</Button>)}</Space>
            </Col>
          </Row>
          <Divider />
          <Typography.Title level={5}>{t('plans.speedSection')}</Typography.Title>
          <FormField label={t('plans.devices')} name="limitIp"><InputNumber min={0} max={1000000} placeholder={t('design.inherit')} style={{ width: '100%' }} /></FormField>

          <Alert
            style={{ marginBottom: 12 }}
            type="info"
            showIcon
            message={preview.length === 0
              ? t('pages.policies.noTiersHint')
              : t('pages.policies.belowFirstHint')}
          />

          {fields.map((field, index) => (
            <Card
              key={field.id}
              size="small"
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
          <Divider />
          <Typography.Title level={5}>{t('plans.inbounds')}</Typography.Title>
          <Typography.Paragraph type="secondary">{t('plans.inboundsHint')}</Typography.Paragraph>
          <Space style={{ marginBottom: 12 }}>
            <Button size="small" onClick={() => methods.setValue('inboundIds', inbounds.map((i) => i.id), { shouldDirty: true })}>{t('plans.selectAll')}</Button>
            <Button size="small" onClick={() => methods.setValue('inboundIds', [], { shouldDirty: true })}>{t('plans.clear')}</Button>
          </Space>
          <FormField name="inboundIds">
            <PlanInboundPicker inbounds={inbounds} />
          </FormField>
          {!inbounds.length && <Alert type="info" title={t('plans.noInbounds')} />}
          <Collapse style={{ marginTop: 16 }} items={[{ key: 'advanced', label: t('plans.advanced'), children: <>
            {reseller && <Alert type="info" title={t('plans.resellerWindow')} style={{ marginBottom: 12 }} />}
            <FormField label={t('plans.firstUse')} name="startOnFirstUse" valueProp="checked"><Switch disabled={reseller} /></FormField>
            <FormField label={t('plans.autoRenew')} name="autoRenewDays"><InputNumber min={0} max={36500} disabled={reseller} /></FormField>
            <Typography.Paragraph type="secondary">{t('plans.autoRenewHint')}</Typography.Paragraph>
            <FormField label={t('plans.resetCycle')} name="resetCycle"><Segmented options={[{ value: 'none', label: t('plans.never') }, { value: 'weekly', label: t('plans.weekly') }, { value: 'monthly', label: t('plans.monthly') }]} /></FormField>
            <FormField label={t('plans.onDepletion')} name="onDepletion"><Select options={[{ value: 'disable', label: t('plans.disable') }, { value: 'throttle', label: t('plans.throttle') }, { value: 'notify', label: t('plans.notify') }]} /></FormField>
            <Alert type="info" title={t('plans.continuationHint')} style={{ marginBottom: 12 }} />
            <Form.Item label={t('plans.burst')}><Switch checked={false} disabled /><Typography.Paragraph type="secondary">{t('plans.burstUnsupported')}</Typography.Paragraph></Form.Item>
            <FormField label={t('plans.archived')} name="archived" valueProp="checked"><Switch /></FormField>
          </> }]} />

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
        </Form>
        </Col>
        <Col xs={24} lg={8}>
          <Card title={t('pages.policies.preview')} size="small" style={{ position: 'sticky', top: 0 }}>
            <Typography.Title level={5}>{draft.name || t('plans.untitled')}</Typography.Title>
            <Typography.Paragraph type="secondary">{draft.description}</Typography.Paragraph>
            {[['plans.quota', draft.quotaBytes == null ? t('design.inherit') : draft.quotaBytes === 0 ? t('pages.policies.unlimited') : SizeFormatter.sizeFormat(draft.quotaBytes)],
              ['plans.duration', draft.durationDays == null ? t('design.inherit') : draft.durationDays === 0 ? t('pages.policies.unlimited') : t('design.days', { count: draft.durationDays })],
              ['plans.devices', draft.limitIp == null ? t('design.inherit') : draft.limitIp === 0 ? t('pages.policies.unlimited') : String(draft.limitIp)],
            ].map(([label, value]) => <Typography.Paragraph key={label}><Typography.Text type="secondary">{t(label)}: </Typography.Text>{value}</Typography.Paragraph>)}
            {preview.map((tier) => <Typography.Paragraph key={tier.fromBytes}><span dir="ltr">{describeTier(tier, t)}</span></Typography.Paragraph>)}
            <Space wrap>{draft.inboundIds?.map((id) => <Tag key={id}>{inbounds.find((i) => i.id === id)?.remark || `#${id}`}</Tag>)}</Space>
            <Divider />
            <Typography.Text type="secondary">{draft.startOnFirstUse ? t('plans.firstUse') : t('plans.startsNow')}</Typography.Text>
            {policy && <Alert type="info" title={t('plans.reapplyHint')} style={{ marginTop: 12 }} />}
          </Card>
        </Col>
        </Row>
      </FormProvider>
    </Modal>
  );
}
