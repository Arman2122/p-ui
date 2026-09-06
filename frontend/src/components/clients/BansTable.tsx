import { Badge, Button, Popconfirm, Table, Typography } from 'antd';
import { useTranslation } from 'react-i18next';

import type { BanView } from '@/generated/zod';
import { IntlUtil } from '@/utils';

export interface BansTableProps {
  bans: BanView[];
  loading?: boolean;
  /* Empty hides the release controls, for a read-only listing. */
  onRelease?: (email: string, source: string) => void;
  onReleaseAll?: () => void;
  releasing?: boolean;
  datepicker?: 'gregorian' | 'jalalian';
}

/* A ban's reason is what the operator reads first: a device over its cap, an
   address they banned themselves, or a shared address reported and left alone. */
function reasonKey(reason: string): string {
  switch (reason) {
    case 'operator': return 'pages.settings.ipLimitBanReasonOperator';
    case 'shared-skipped': return 'pages.settings.ipLimitBanReasonReported';
    default: return 'pages.settings.ipLimitBanReasonOverCap';
  }
}

export function BansTable({ bans, loading, onRelease, onReleaseAll, releasing, datepicker = 'gregorian' }: BansTableProps) {
  const { t } = useTranslation();
  const columns = [
    {
      title: t('pages.clients.email'),
      dataIndex: 'clientEmail',
      key: 'client',
      render: (email: string) => email || <Typography.Text type="secondary">—</Typography.Text>,
    },
    {
      title: t('pages.settings.ipLimitBanSource'),
      dataIndex: 'source',
      key: 'source',
      render: (source: string) => <Typography.Text code>{source}</Typography.Text>,
    },
    {
      title: t('pages.settings.ipLimitBanWhere'),
      key: 'where',
      render: (_: unknown, row: BanView) => row.node || t('pages.settings.ipLimitBanHere'),
    },
    {
      title: t('pages.settings.ipLimitBanPorts'),
      dataIndex: 'ports',
      key: 'ports',
      render: (ports: string, row: BanView) => {
        if (row.reason === 'shared-skipped') return <Typography.Text type="secondary">—</Typography.Text>;
        return ports === '*' ? t('pages.settings.ipLimitBanAllPorts') : ports;
      },
    },
    {
      title: t('pages.settings.ipLimitBanReason'),
      dataIndex: 'reason',
      key: 'reason',
      /* A status dot and plain text: a coloured tag's preset palette fails the
         contrast rule the Storybook a11y check enforces. */
      render: (reason: string) => (
        <Badge
          status={reason === 'shared-skipped' ? 'default' : reason === 'operator' ? 'processing' : 'error'}
          text={t(reasonKey(reason))}
        />
      ),
    },
    {
      title: t('pages.settings.ipLimitBanUntil'),
      dataIndex: 'until',
      key: 'until',
      render: (until: number) => IntlUtil.formatDate(until * 1000, datepicker),
    },
    ...(onRelease
      ? [{
        title: t('pages.settings.ipLimitRelease'),
        key: 'release',
        render: (_: unknown, row: BanView) => (
          <Button size="small" onClick={() => onRelease(row.clientEmail, row.source)} disabled={releasing}>
            {t('pages.settings.ipLimitRelease')}
          </Button>
        ),
      }]
      : []),
  ];

  return (
    <div>
      {onReleaseAll ? (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
          <Popconfirm title={t('pages.settings.ipLimitReleaseAllConfirm')} onConfirm={onReleaseAll} disabled={bans.length === 0}>
            <Button danger size="small" disabled={bans.length === 0 || releasing}>
              {t('pages.settings.ipLimitReleaseAll')}
            </Button>
          </Popconfirm>
        </div>
      ) : null}
      <Table<BanView>
        size="small"
        rowKey="id"
        loading={loading}
        columns={columns}
        dataSource={bans}
        pagination={bans.length > 10 ? { pageSize: 10 } : false}
        /* Own text, not antd's Empty: its description grey fails the contrast rule. */
        locale={{ emptyText: <Typography.Text>{t('pages.settings.ipLimitBansNone')}</Typography.Text> }}
        scroll={{ x: true }}
      />
    </div>
  );
}
