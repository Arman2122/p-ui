import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Form, Modal, Select, Typography, message } from 'antd';

import { SelectAllClearButtons } from '@/components/form';
import type { InboundOption } from '@/hooks/useClients';
import { formatInboundLabel } from '@/lib/inbounds/label';
import { supportsMultipleClients } from '@/lib/inbounds/multi-client';
import type { BulkMoveResult } from '@/schemas/client';

interface BulkMoveInboundsModalProps {
  open: boolean;
  count: number;
  inbounds: InboundOption[];
  onOpenChange: (open: boolean) => void;
  onSubmit: (fromInboundIds: number[], toInboundIds: number[]) => Promise<BulkMoveResult | null>;
}

export default function BulkMoveInboundsModal({ open, count, inbounds, onOpenChange, onSubmit }: BulkMoveInboundsModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [fromIds, setFromIds] = useState<number[]>([]);
  const [toIds, setToIds] = useState<number[]>([]);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      setFromIds([]);
      setToIds([]);
    }
  }, [open]);

  const options = useMemo(() => {
    return (inbounds || [])
      .filter((ib) => supportsMultipleClients(ib.protocol))
      .map((ib) => ({ value: ib.id, label: formatInboundLabel(ib.tag, ib.remark) }));
  }, [inbounds]);
  /* An inbound cannot be both the source and the target of one move. */
  const fromOptions = useMemo(() => options.filter((o) => !toIds.includes(o.value)), [options, toIds]);
  const toOptions = useMemo(() => options.filter((o) => !fromIds.includes(o.value)), [options, fromIds]);
  const ready = fromIds.length > 0 && toIds.length > 0 && count > 0;

  async function submit() {
    if (!ready) return;
    setSubmitting(true);
    try {
      const result = await onSubmit(fromIds, toIds);
      if (!result) return;
      messageApi.success(t('pages.clients.toasts.bulkMoved', { count: result.moved ?? 0 }));
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      {messageContextHolder}
      <Modal
        open={open}
        title={t('pages.clients.moveTitle', { count })}
        okText={t('pages.clients.move')}
        cancelText={t('cancel')}
        okButtonProps={{ disabled: !ready, loading: submitting }}
        onCancel={() => onOpenChange(false)}
        onOk={submit}
        destroyOnHidden
      >
        <Typography.Paragraph type="secondary">{t('pages.clients.moveDesc')}</Typography.Paragraph>
        {options.length < 2 ? (
          <Alert type="info" showIcon title={t('pages.clients.moveNoTargets')} />
        ) : (
          <Form layout="vertical">
            <Form.Item label={t('pages.clients.moveFrom')}>
              <SelectAllClearButtons options={fromOptions} value={fromIds} onChange={setFromIds} />
              <Select
                mode="multiple"
                style={{ width: '100%' }}
                value={fromIds}
                onChange={setFromIds}
                options={fromOptions}
                showSearch={{ optionFilterProp: 'label' }}
                autoFocus
              />
            </Form.Item>
            <Form.Item label={t('pages.clients.moveTo')}>
              <SelectAllClearButtons options={toOptions} value={toIds} onChange={setToIds} />
              <Select
                mode="multiple"
                style={{ width: '100%' }}
                value={toIds}
                onChange={setToIds}
                options={toOptions}
                showSearch={{ optionFilterProp: 'label' }}
              />
            </Form.Item>
          </Form>
        )}
      </Modal>
    </>
  );
}
