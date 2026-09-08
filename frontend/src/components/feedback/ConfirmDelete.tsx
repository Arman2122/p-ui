import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, Modal, Space } from 'antd';

import { Icon } from '@/components/ui/Icon';
import { Kicker } from '@/components/ui/panel';

export interface ConfirmDeleteProps {
  open: boolean;
  title: ReactNode;
  /** One line under the title saying what the action actually touches. */
  what?: ReactNode;
  /** Consequences, one per bullet. Say what breaks, not that it is irreversible. */
  lines?: ReactNode[];
  /** When set, the confirm button stays disabled until this word is typed. */
  requireText?: string | null;
  okText?: string;
  danger?: boolean;
  confirmLoading?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

/*
A destructive confirmation that spells out the blast radius. The typed word is
for sweeps big enough that a mis-click is expensive; a single delete does not
earn it, and asking for one every time trains people to type it without reading.
*/
export default function ConfirmDelete({
  open,
  title,
  what,
  lines = [],
  requireText,
  okText,
  danger = true,
  confirmLoading = false,
  onCancel,
  onConfirm,
}: ConfirmDeleteProps) {
  const { t } = useTranslation();
  const [typed, setTyped] = useState('');

  useEffect(() => {
    if (open) setTyped('');
  }, [open]);

  const satisfied = !requireText || typed.trim().toUpperCase() === requireText.toUpperCase();
  const tone = danger ? 'var(--p-error)' : 'var(--p-warning)';

  return (
    <Modal
      open={open}
      onCancel={onCancel}
      width={460}
      destroyOnHidden
      closable={false}
      title={
        <div style={{ display: 'flex', gap: 11, alignItems: 'flex-start' }}>
          <span
            className="p-alert-i"
            style={{ background: `color-mix(in srgb, ${tone} 15%, transparent)`, color: tone, flex: '0 0 28px', width: 28, height: 28 }}
          >
            <Icon name={danger ? 'trash' : 'power'} size={14} />
          </span>
          <span style={{ minWidth: 0 }}>
            <span style={{ display: 'block', fontSize: 15, fontWeight: 650, color: 'var(--p-text)' }}>{title}</span>
            {what && <span className="p-sub" style={{ display: 'block', marginTop: 2, whiteSpace: 'normal' }}>{what}</span>}
          </span>
        </div>
      }
      footer={
        <Space size={8}>
          <Button onClick={onCancel}>{t('cancel')}</Button>
          <Button type="primary" danger={danger} disabled={!satisfied} loading={confirmLoading} onClick={onConfirm}>
            {okText ?? t('delete')}
          </Button>
        </Space>
      }
    >
      {lines.length > 0 && (
        <ul className="p-conflist">
          {lines.map((line, i) => <li key={i}>{line}</li>)}
        </ul>
      )}
      {requireText && (
        <div style={{ marginTop: 14 }}>
          <Kicker>{t('typeToConfirm', { text: requireText })}</Kicker>
          <Input
            style={{ marginTop: 7 }}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={requireText}
            aria-label={t('typeToConfirm', { text: requireText })}
            autoFocus
          />
        </div>
      )}
    </Modal>
  );
}
