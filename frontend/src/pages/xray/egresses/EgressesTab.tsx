import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Popconfirm, Space, Switch, Table, Tag, Typography } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';

import { useEgressesQuery, useEgressPreflightQuery } from '@/api/queries/useEgressesQuery';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import { EGRESS_TYPE, UplinkSettingsSchema, type EgressRecord } from '@/schemas/api/egress';

import EgressFormModal from './EgressFormModal';

/* Where an uplink goes is its endpoint, not a target tag, so the column that
   answers "where does this leave through" stays meaningful for both types. */
function uplinkEndpoint(row: EgressRecord): string {
  if (!row.settings) return '';
  try {
    const parsed = UplinkSettingsSchema.safeParse(JSON.parse(row.settings));
    return parsed.success ? parsed.data.endpoint : '';
  } catch {
    return '';
  }
}

interface EgressesTabProps {
  isMobile: boolean;
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

export default function EgressesTab({ isMobile }: EgressesTabProps) {
  const { t } = useTranslation();
  const egressesQuery = useEgressesQuery();
  const preflightQuery = useEgressPreflightQuery();
  const { update, remove } = useEgressMutations();
  const [editing, setEditing] = useState<EgressRecord | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  const rows = egressesQuery.data ?? [];
  const preflight = preflightQuery.data;

  function openForm(egress: EgressRecord | null) {
    setEditing(egress);
    setFormOpen(true);
  }

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
      title: t('pages.xray.egress.type'),
      dataIndex: 'type',
      render: (type: string) => (
        <Tag bordered={false} color={type === EGRESS_TYPE ? 'blue' : 'green'}>
          {t(`pages.xray.egress.types.${type}`, { defaultValue: type })}
        </Tag>
      ),
    },
    {
      title: t('pages.xray.egress.target'),
      dataIndex: 'target',
      render: (target: string, row) => {
        const where = target || uplinkEndpoint(row);
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
            onClick={() => openForm(row)}
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
    <>
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

      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => openForm(null)}>
          {t('pages.xray.egress.add')}
        </Button>
      </Space>

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

      <EgressFormModal open={formOpen} egress={editing} onClose={() => setFormOpen(false)} />
    </>
  );
}
