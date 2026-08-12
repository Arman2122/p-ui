import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Card, Col, Form, Input, InputNumber, Modal, Row, Segmented, Select, Space, Typography } from 'antd';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { FormProvider, useFieldArray, useWatch } from 'react-hook-form';

import { FormField, useZodForm } from '@/components/form/rhf';
import { usePolicyMutations } from '@/api/queries/usePolicyMutations';
import { describeTier } from '@/lib/policies/labels';
import {
  PolicyFormSchema,
  RATE_UNITS,
  emptyTierRow,
  parseTiers,
  tierRowsFromWire,
  tierRowsToWire,
  type PolicyFormValues,
  type PolicyRecord,
} from '@/schemas/api/policy';

interface PolicyFormModalProps {
  open: boolean;
  policy: PolicyRecord | null;
  onClose: () => void;
}

const UNIT_OPTIONS = RATE_UNITS.map((u) => ({ label: u.label, value: u.key }));

function defaultValues(): PolicyFormValues {
  return { id: 0, name: '', tiers: [emptyTierRow(0)] };
}

/* Unlimited is picked, never typed: a rate field left at zero and a rate field
   meaning "no cap" are the same keystroke otherwise. */
function DirectionFields({ index, dir, label }: { index: number; dir: 'up' | 'down'; label: string }) {
  const { t } = useTranslation();
  const limited = useWatch({ name: `tiers.${index}.${dir}Limited` }) as boolean | undefined;

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
        <Space.Compact style={{ display: 'flex' }}>
          <FormField
            name={`tiers.${index}.${dir}Value`}
            noStyle
            transform={{ output: (v) => Number(v) || 0 }}
          >
            <InputNumber min={0} step={1} style={{ flex: 1 }} placeholder={t('pages.policies.ratePlaceholder')} />
          </FormField>
          <FormField name={`tiers.${index}.${dir}Unit`} noStyle>
            <Select options={UNIT_OPTIONS} style={{ width: 96 }} />
          </FormField>
        </Space.Compact>
      )}
    </Space>
  );
}

export default function PolicyFormModal({ open, policy, onClose }: PolicyFormModalProps) {
  const { t } = useTranslation();
  const methods = useZodForm(PolicyFormSchema, { defaultValues: defaultValues() });
  const { fields, append, remove } = useFieldArray({ control: methods.control, name: 'tiers' });
  const [submitting, setSubmitting] = useState(false);
  const { add, update } = usePolicyMutations();
  const tiers = useWatch({ control: methods.control, name: 'tiers' });

  useEffect(() => {
    if (!open) return;
    methods.reset(policy
      ? { id: policy.id, name: policy.name, tiers: tierRowsFromWire(parseTiers(policy.tiers)) }
      : defaultValues());
  }, [open, policy, methods]);

  const preview = tierRowsToWire(tiers ?? []);
  const lowest = preview.length > 0 ? preview[0].fromBytes : 0;

  async function onFinish(form: PolicyFormValues) {
    setSubmitting(true);
    try {
      const payload = { name: form.name.trim(), tiers: tierRowsToWire(form.tiers) };
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
      width="760px"
      styles={{ body: { maxHeight: 'calc(100vh - 220px)', overflowY: 'auto' } }}
      onOk={methods.handleSubmit(onFinish)}
      onCancel={() => { if (!submitting) onClose(); }}
    >
      <FormProvider {...methods}>
        <Form layout="vertical">
          <FormField label={t('pages.policies.name')} name="name" required>
            <Input placeholder={t('pages.policies.namePlaceholder')} />
          </FormField>

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
                  <FormField
                    name={`tiers.${index}.fromGB`}
                    label={t('pages.policies.threshold')}
                    tooltip={t('pages.policies.thresholdHint')}
                    transform={{ output: (v) => Number(v) || 0 }}
                  >
                    <InputNumber min={0} step={1} addonAfter="GB" style={{ width: '100%' }} />
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
        </Form>
      </FormProvider>
    </Modal>
  );
}
