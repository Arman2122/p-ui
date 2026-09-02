import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, AutoComplete, Form, Input, InputNumber, Modal, Segmented, Select, Space, message } from 'antd';
import dayjs from 'dayjs';

import { DateTimePicker } from '@/components/form';
import {
  EMPTY_BULK_SET,
  buildBulkSetPatch,
  firstBulkSetIssue,
  type BulkSetFormValues,
  type BulkSetPatch,
  type TriField,
  type TriMode,
} from '@/lib/clients/bulk-set';
import type { BulkSetResult } from '@/schemas/client';
import { TLS_FLOW_CONTROL } from '@/schemas/primitives/flow';

interface ClientBulkSetModalProps {
  open: boolean;
  count: number;
  groups: string[];
  policies: { id: number; name: string }[];
  onOpenChange: (open: boolean) => void;
  onSubmit: (patch: BulkSetPatch) => Promise<BulkSetResult | null>;
}

type RowKey = keyof BulkSetFormValues;

export default function ClientBulkSetModal({ open, count, groups, policies, onOpenChange, onSubmit }: ClientBulkSetModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [values, setValues] = useState<BulkSetFormValues>(EMPTY_BULK_SET);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) setValues(EMPTY_BULK_SET);
  }, [open]);

  function setMode(key: RowKey, mode: TriMode) {
    setValues((prev) => ({ ...prev, [key]: { ...prev[key], mode } }));
  }

  function setValue<K extends RowKey>(key: K, value: BulkSetFormValues[K]['value']) {
    setValues((prev) => ({ ...prev, [key]: { ...prev[key], value } }));
  }

  const modeOptions: { label: string; value: TriMode }[] = [
    { label: t('pages.clients.bulkSetKeep'), value: 'keep' },
    { label: t('pages.clients.bulkSetSet'), value: 'set' },
    { label: t('pages.clients.bulkSetClear'), value: 'clear' },
  ];

  function row(key: RowKey, label: string, field: TriField<unknown>, control: React.ReactNode) {
    return (
      <Form.Item label={label} style={{ marginBottom: 12 }}>
        <Space direction="vertical" style={{ width: '100%' }} size={4}>
          <Segmented<TriMode>
            size="small"
            options={modeOptions}
            value={field.mode}
            onChange={(mode) => setMode(key, mode)}
            data-testid={`bulk-set-${key}`}
          />
          {field.mode === 'set' ? control : null}
        </Space>
      </Form.Item>
    );
  }

  async function handleOk() {
    const issue = firstBulkSetIssue(values);
    if (issue) {
      messageApi.warning(t(issue));
      return;
    }
    setSubmitting(true);
    try {
      const result = await onSubmit(buildBulkSetPatch(values));
      if (!result) return;
      messageApi.success(t('pages.clients.toasts.bulkSet', { count: result.updated ?? 0 }));
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  }

  const expiry = values.expiryTime.value ? dayjs(values.expiryTime.value) : null;

  return (
    <>
      {messageContextHolder}
      <Modal
        open={open}
        title={t('pages.clients.bulkSetTitle', { count })}
        okText={t('pages.clients.bulkSetSet')}
        cancelText={t('cancel')}
        okButtonProps={{ loading: submitting, disabled: count === 0 }}
        onCancel={() => onOpenChange(false)}
        onOk={handleOk}
        destroyOnHidden
      >
        <Alert type="info" showIcon title={t('pages.clients.bulkSetHint')} style={{ marginBottom: 16 }} />
        <Form layout="vertical">
          {row('limitIp', t('pages.clients.limitIp'), values.limitIp, (
            <InputNumber min={0} precision={0} style={{ width: '100%' }} value={values.limitIp.value} onChange={(v) => setValue('limitIp', Number(v) || 0)} />
          ))}
          {row('totalGB', t('pages.clients.totalGB'), values.totalGB, (
            <InputNumber min={0} step={1} style={{ width: '100%' }} value={values.totalGB.value} onChange={(v) => setValue('totalGB', Number(v) || 0)} />
          ))}
          {row('expiryTime', t('pages.clients.expiryTime'), values.expiryTime, (
            <DateTimePicker value={expiry} onChange={(next) => setValue('expiryTime', next ? next.valueOf() : null)} />
          ))}
          {row('reset', t('pages.clients.renewDays'), values.reset, (
            <InputNumber min={0} precision={0} style={{ width: '100%' }} value={values.reset.value} onChange={(v) => setValue('reset', Number(v) || 0)} />
          ))}
          {row('flow', t('pages.clients.flow'), values.flow, (
            <Select
              style={{ width: '100%' }}
              value={values.flow.value || undefined}
              onChange={(v) => setValue('flow', v)}
              options={Object.values(TLS_FLOW_CONTROL).map((k) => ({ value: k, label: k }))}
            />
          ))}
          {row('group', t('pages.clients.group'), values.group, (
            <AutoComplete
              style={{ width: '100%' }}
              value={values.group.value}
              onChange={(v) => setValue('group', v)}
              options={groups.map((g) => ({ value: g }))}
              filterOption={(input, option) => (option?.value ?? '').toLowerCase().includes(input.toLowerCase())}
            />
          ))}
          {row('policyId', t('pages.clients.bulkSetPlan'), values.policyId, (
            <Select
              style={{ width: '100%' }}
              value={values.policyId.value ?? undefined}
              onChange={(v) => setValue('policyId', v)}
              options={policies.map((p) => ({ value: p.id, label: p.name }))}
              showSearch={{ optionFilterProp: 'label' }}
            />
          ))}
          {row('tgId', t('pages.clients.telegramId'), values.tgId, (
            <InputNumber min={0} precision={0} style={{ width: '100%' }} value={values.tgId.value} onChange={(v) => setValue('tgId', Number(v) || 0)} />
          ))}
          {row('comment', t('pages.clients.comment'), values.comment, (
            <Input value={values.comment.value} onChange={(e) => setValue('comment', e.target.value)} />
          ))}
        </Form>
      </Modal>
    </>
  );
}
