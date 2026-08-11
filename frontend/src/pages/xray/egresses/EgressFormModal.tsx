import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Form, Input, Modal, Select, Switch, Tag } from 'antd';
import { FormProvider, useForm } from 'react-hook-form';

import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { useOutboundTagGroups } from '@/api/queries/useOutboundTags';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import {
  EGRESS_TYPE,
  EgressFormSchema,
  type EgressFormValues,
  type EgressRecord,
} from '@/schemas/api/egress';

interface EgressFormModalProps {
  open: boolean;
  egress: EgressRecord | null;
  onClose: () => void;
}

function defaultValues(): EgressFormValues {
  return { id: 0, remark: '', target: '', enable: true };
}

export default function EgressFormModal({ open, egress, onClose }: EgressFormModalProps) {
  const { t } = useTranslation();
  const methods = useForm<EgressFormValues>({ defaultValues: defaultValues() });
  const [submitting, setSubmitting] = useState(false);
  const { add, update } = useEgressMutations();
  const { data: outboundGroups } = useOutboundTagGroups();

  /* Blackhole stays in the list on purpose: an egress pointed at it is a
     deliberately dark exit, which the backend accepts as a resolvable target. */
  const targetOptions = useMemo<
    ({ label: string; value: string } | { label: string; options: { label: string; value: string }[] })[]
  >(() => {
    const outbounds = (outboundGroups?.outbounds ?? []).map((tag) => ({ label: tag, value: tag }));
    if (!outboundGroups?.balancers.length) return outbounds;
    return [
      { label: t('pages.xray.Outbounds'), options: outbounds },
      { label: t('pages.xray.Balancers'), options: outboundGroups.balancers.map((tag) => ({ label: tag, value: tag })) },
    ];
  }, [outboundGroups, t]);

  useEffect(() => {
    if (!open) return;
    methods.reset(egress
      ? { id: egress.id, remark: egress.remark, target: egress.target, enable: egress.enable }
      : defaultValues());
  }, [open, egress, methods]);

  async function onFinish(values: EgressFormValues) {
    const result = EgressFormSchema.safeParse(values);
    if (!result.success) return;
    const form = result.data;
    setSubmitting(true);
    try {
      const payload = {
        id: form.id,
        type: EGRESS_TYPE,
        enable: form.enable,
        remark: form.remark,
        target: form.target,
        settings: egress?.settings ?? '',
      };
      const msg = egress ? await update(egress.id, payload) : await add(payload);
      if (msg?.success) onClose();
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal
      open={open}
      title={egress ? t('pages.xray.egress.edit') : t('pages.xray.egress.add')}
      confirmLoading={submitting}
      okText={t('save')}
      cancelText={t('cancel')}
      mask={{ closable: false }}
      width="520px"
      onOk={methods.handleSubmit(onFinish)}
      onCancel={() => { if (!submitting) onClose(); }}
    >
      <FormProvider {...methods}>
        <Form layout="vertical">
          <Form.Item label={t('pages.xray.egress.type')} tooltip={t('pages.xray.egress.typeHint')}>
            <Tag bordered={false} style={{ fontFamily: 'monospace' }}>{EGRESS_TYPE}</Tag>
          </Form.Item>

          <FormField label={t('remark')} name="remark">
            <Input placeholder={t('pages.xray.egress.remarkPlaceholder')} />
          </FormField>

          <FormField
            label={t('pages.xray.egress.target')}
            name="target"
            tooltip={t('pages.xray.egress.targetHint')}
            required
            rules={{ validate: rhfZodValidate(EgressFormSchema.shape.target) }}
          >
            <Select
              showSearch
              placeholder={t('pages.xray.egress.targetPlaceholder')}
              options={targetOptions}
            />
          </FormField>

          <FormField
            label={t('enable')}
            name="enable"
            valueProp="checked"
            extra={t('pages.xray.egress.enableHint')}
          >
            <Switch />
          </FormField>
        </Form>
      </FormProvider>
    </Modal>
  );
}
