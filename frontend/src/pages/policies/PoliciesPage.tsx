import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Popconfirm, Space, Table, Tag, Typography } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';

import { usePoliciesQuery } from '@/api/queries/usePoliciesQuery';
import { usePolicyMutations } from '@/api/queries/usePolicyMutations';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { shapeableKinds } from '@/lib/cores/client-shaping';
import { describeTier } from '@/lib/policies/labels';
import { parseTiers, type PolicyRecord } from '@/schemas/api/policy';

import PolicyFormModal from './PolicyFormModal';

export default function PoliciesPage() {
  const { t } = useTranslation();
  const policiesQuery = usePoliciesQuery();
  const coresQuery = useCoresQuery();
  const { remove } = usePolicyMutations();
  const [editing, setEditing] = useState<PolicyRecord | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  const rows = policiesQuery.data ?? [];
  const shapeable = useMemo(() => shapeableKinds(coresQuery.data ?? []), [coresQuery.data]);

  function openForm(policy: PolicyRecord | null) {
    setEditing(policy);
    setFormOpen(true);
  }

  const columns: ColumnsType<PolicyRecord> = [
    {
      title: t('pages.policies.name'),
      dataIndex: 'name',
      render: (_: unknown, row) => (
        <Space direction="vertical" size={0}>
          <span>{row.name}</span>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            <span dir="ltr">#{row.id}</span>
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: t('pages.policies.ladder'),
      dataIndex: 'tiers',
      render: (raw: unknown) => {
        const tiers = parseTiers(raw);
        if (tiers.length === 0) {
          return <Typography.Text type="secondary">{t('pages.policies.noTiersHint')}</Typography.Text>;
        }
        return (
          <Space direction="vertical" size={2}>
            {tiers[0].fromBytes > 0 && (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('pages.policies.belowFirstHint')}
              </Typography.Text>
            )}
            {tiers.map((tier) => (
              <span key={tier.fromBytes} dir="ltr">{describeTier(tier, t)}</span>
            ))}
          </Space>
        );
      },
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
            aria-label={t('pages.policies.edit')}
            onClick={() => openForm(row)}
          />
          <Popconfirm
            title={t('pages.policies.deleteConfirm')}
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
        <h4>{t('pages.policies.title')}</h4>
        <p>{t('pages.policies.desc')}</p>
      </div>

      {/* A kind names a core's protocol, so it is rendered as an identifier rather than translated. */}
      <Alert
        style={{ marginBottom: 12 }}
        type={shapeable.length > 0 ? 'info' : 'warning'}
        showIcon
        message={shapeable.length > 0 ? t('pages.policies.ratesReach') : t('pages.policies.ratesReachNothing')}
        description={shapeable.length > 0 ? (
          <Space size={4} wrap>
            {shapeable.map((kind) => <Tag key={kind} bordered={false}><span dir="ltr">{kind}</span></Tag>)}
          </Space>
        ) : undefined}
      />

      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => openForm(null)}>
          {t('pages.policies.add')}
        </Button>
      </Space>

      <Table<PolicyRecord>
        rowKey="id"
        columns={columns}
        dataSource={rows}
        loading={policiesQuery.isPending}
        pagination={false}
        scroll={{ x: 'max-content' }}
        locale={{ emptyText: t('pages.policies.empty') }}
      />

      <PolicyFormModal open={formOpen} policy={editing} onClose={() => setFormOpen(false)} />
    </>
  );
}
