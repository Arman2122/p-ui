import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { InputNumber, Segmented, Space, Tooltip, Typography } from 'antd';

export interface IpLimitControlProps {
  /* The stored value, where 0 means unlimited. */
  value: number;
  onChange: (value: number) => void;
  /* True when this host cannot enforce the cap at all, which is a third state
     and never the same thing as having no cap. */
  disabled?: boolean;
  notice?: string;
  /* Rendered beside the number, for the IP-log button the client form adds. */
  addon?: ReactNode;
}

const DEFAULT_LIMIT = 2;

/*
Two states the operator picks between, because 0 and "no cap" are the same
keystroke on a bare number field and read as opposites.
*/
export function IpLimitControl({ value, onChange, disabled, notice, addon }: IpLimitControlProps) {
  const { t } = useTranslation();
  const limited = value > 0;

  const control = (
    <Space direction="vertical" size={4} style={{ width: '100%' }}>
      <Segmented
        block
        value={limited ? 'limited' : 'unlimited'}
        disabled={disabled}
        options={[
          { label: t('pages.policies.unlimited'), value: 'unlimited' },
          { label: t('pages.clients.limitIpCapped'), value: 'limited' },
        ]}
        onChange={(next) => onChange(next === 'limited' ? (value > 0 ? value : DEFAULT_LIMIT) : 0)}
      />
      {limited && (
        <Space.Compact style={{ display: 'flex' }}>
          <InputNumber
            value={value}
            min={1}
            step={1}
            disabled={disabled}
            style={{ flex: 1 }}
            aria-label={t('pages.clients.limitIp')}
            onChange={(v) => onChange(Math.max(1, Number(v) || 1))}
          />
          {addon}
        </Space.Compact>
      )}
      {!limited && addon && <Space.Compact style={{ display: 'flex' }}>{addon}</Space.Compact>}
      {disabled && notice && (
        <Typography.Text type="warning" style={{ fontSize: 12 }}>{notice}</Typography.Text>
      )}
    </Space>
  );

  return disabled && notice ? <Tooltip title={notice}>{control}</Tooltip> : control;
}

export default IpLimitControl;
