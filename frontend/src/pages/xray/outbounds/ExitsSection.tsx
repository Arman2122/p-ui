import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Popconfirm, Space, Switch, Table, Typography } from 'antd';
import { DeleteOutlined, EditOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';

import { useEgressesQuery, useEgressPreflightQuery } from '@/api/queries/useEgressesQuery';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import { UplinkSettingsSchema, type EgressRecord } from '@/schemas/api/egress';

import ExitFormModal from './ExitFormModal';

/* Where an exit goes is its endpoint, not a target tag. */
function endpointOf(row: EgressRecord): string {
  if (!row.settings) return '';
  try {
    const parsed = UplinkSettingsSchema.safeParse(JSON.parse(row.settings));
    return parsed.success ? parsed.data.endpoint : '';
  } catch {
    return '';
  }
}

/* Backend sentences name a sysctl, a device or a rule priority, so they are
   shown verbatim and left-to-right even when the panel is in Persian. */
function HostSentences({ lines }: { lines: string[] }) {
  return (
    <div dir="ltr" style={{ textAlign: 'left' }}>
      {lines.map((line) => <div key={line}><code>{line}</code></div>)}
    </div>
  );
}

interface ExitsSectionProps {
  isMobile: boolean;
}

export default function ExitsSection({ isMobile }: ExitsSectionProps) {
  const { t } = useTranslation();
  const egressesQuery = useEgressesQuery();
  const preflightQuery = useEgressPreflightQuery();
  const { update, remove } = useEgressMutations();
  const [editing, setEditing] = useState<EgressRecord | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  /* Panel-owned rows are the fronts routing provisions and reaps for itself, so
     an operator neither made them nor can act on them. */
  const rows = (egressesQuery.data ?? []).filter((row) => row.owner !== 'panel');
  const preflight = preflightQuery.data;

  function toggleEnable(row: EgressRecord, enable: boolean) {
    void update(row.id, {
      id: row.id,
      type: row.type,
      enable,
      remark: row.remark,
      target: row.target,
      settings: row.settings,
    });
  }

  const columns: ColumnsType<EgressRecord> = [
    {
      title: t('remark'),
      dataIndex: 'remark',
      render: (_: unknown, row) => (
        <Space direction="vertical" size={0}>
          <span>{row.remark || t('none')}</span>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            <span dir="ltr">#{row.id}</span>
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: t('pages.xray.egress.target'),
      dataIndex: 'target',
      render: (target: string, row) => {
        const where = target || endpointOf(row);
        return where
          ? <code dir="ltr">{where}</code>
          : <Typography.Text type="secondary">{t('none')}</Typography.Text>;
      },
    },
    {
      title: t('enable'),
      dataIndex: 'enable',
      render: (_: unknown, row) => (
        <Switch
          checked={row.enable}
          size={isMobile ? 'small' : 'default'}
          onChange={(checked) => toggleEnable(row, checked)}
        />
      ),
    },
    {
      title: '',
      key: 'actions',
      align: 'end',
      render: (_: unknown, row) => (
        <Space>
          <Button
            size="small"
            icon={<EditOutlined />}
            aria-label={t('pages.xray.egress.edit')}
            onClick={() => { setEditing(row); setFormOpen(true); }}
          />
          <Popconfirm
            title={t('pages.xray.egress.deleteConfirm')}
            okText={t('confirm')}
            cancelText={t('cancel')}
            onConfirm={() => { void remove(row.id); }}
          >
            <Button size="small" danger icon={<DeleteOutlined />} aria-label={t('remove')} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ marginTop: 24 }}>
      <div style={{ marginBottom: 16 }}>
        <h4>{t('pages.xray.egress.title')}</h4>
        <p>{t('pages.xray.egress.desc')}</p>
      </div>

      {preflight && !preflight.ok && (
        <Alert
          style={{ marginBottom: 12 }}
          type="error"
          showIcon
          title={t('pages.xray.egress.preflightBlocked')}
          description={<HostSentences lines={preflight.refusals} />}
        />
      )}
      {preflight && preflight.notes.length > 0 && (
        <Alert
          style={{ marginBottom: 12 }}
          type="warning"
          showIcon
          title={t('pages.xray.egress.preflightNotes')}
          description={<HostSentences lines={preflight.notes} />}
        />
      )}

      <Table<EgressRecord>
        rowKey="id"
        size={isMobile ? 'small' : 'middle'}
        columns={columns}
        dataSource={rows}
        loading={egressesQuery.isPending}
        pagination={false}
        scroll={{ x: 'max-content' }}
        locale={{ emptyText: t('pages.xray.egress.empty') }}
      />

      <ExitFormModal open={formOpen} exit={editing} onClose={() => setFormOpen(false)} />
    </div>
  );
}
