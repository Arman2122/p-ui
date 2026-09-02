import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Checkbox, Modal, Space, message } from 'antd';

import type { BulkRotateResult } from '@/schemas/client';

interface BulkRotateModalProps {
  open: boolean;
  count: number;
  onOpenChange: (open: boolean) => void;
  onSubmit: (credentials: boolean, subId: boolean) => Promise<BulkRotateResult | null>;
}

export default function BulkRotateModal({ open, count, onOpenChange, onSubmit }: BulkRotateModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [credentials, setCredentials] = useState(true);
  const [subId, setSubId] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      setCredentials(true);
      setSubId(false);
    }
  }, [open]);

  const ready = (credentials || subId) && count > 0;

  async function submit() {
    if (!ready) return;
    setSubmitting(true);
    try {
      const result = await onSubmit(credentials, subId);
      if (!result) return;
      messageApi.success(t('pages.clients.toasts.bulkRotated', { count: result.rotated ?? 0 }));
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
        title={t('pages.clients.rotateTitle', { count })}
        okText={t('pages.clients.rotateConfirm')}
        okButtonProps={{ danger: true, disabled: !ready, loading: submitting }}
        cancelText={t('cancel')}
        onCancel={() => onOpenChange(false)}
        onOk={submit}
        destroyOnHidden
      >
        <Alert type="warning" showIcon title={t('pages.clients.rotateWarning')} style={{ marginBottom: 16 }} />
        <Space direction="vertical">
          <Checkbox checked={credentials} onChange={(e) => setCredentials(e.target.checked)} data-testid="rotate-credentials">
            {t('pages.clients.rotateCredentials')}
          </Checkbox>
          <Checkbox checked={subId} onChange={(e) => setSubId(e.target.checked)} data-testid="rotate-subid">
            {t('pages.clients.rotateSubId')}
          </Checkbox>
        </Space>
      </Modal>
    </>
  );
}
