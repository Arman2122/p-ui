import { useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Descriptions, Form, Select, Space, Tag, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

import { SizeFormatter } from '@/utils';
import { useEnforcedLimitsQuery, usePoliciesQuery } from '@/api/queries/usePoliciesQuery';
import { shapingForKinds } from '@/lib/cores/client-shaping';
import { formatRate } from '@/lib/policies/labels';
import type { CoreView } from '@/generated/zod';

export const POLICY_UNLOADED = -1;
export const POLICY_NONE = 0;

export interface ClientPlanPanelProps {
  email: string;
  /* One entry per attached inbound, so two shaped inbounds can be counted. */
  kinds: string[];
  cores: CoreView[] | undefined;
  value: number;
  onSeed: (policyId: number) => void;
  onSelect: (policyId: number) => void;
}

/* Kinds name a core's protocols, so they are rendered as identifiers and stay
   left-to-right in Persian. */
function KindTags({ kinds }: { kinds: string[] }) {
  return (
    <Space size={4} wrap>
      {kinds.map((kind) => <Tag key={kind} bordered={false}><span dir="ltr">{kind}</span></Tag>)}
    </Space>
  );
}

export default function ClientPlanPanel({ email, kinds, cores, value, onSeed, onSelect }: ClientPlanPanelProps) {
  const { t } = useTranslation();
  const policiesQuery = usePoliciesQuery();
  const enforcedQuery = useEnforcedLimitsQuery(email);
  const enforced = enforcedQuery.data;

  useEffect(() => {
    if (enforced && value === POLICY_UNLOADED) onSeed(enforced.policyId);
  }, [enforced, value, onSeed]);

  const verdict = useMemo(() => shapingForKinds(cores ?? [], kinds), [cores, kinds]);
  const shapedInbounds = kinds.filter((kind) => verdict.shapeable.includes(kind)).length;
  const options = useMemo(() => [
    { label: t('pages.policies.noPlan'), value: POLICY_NONE },
    ...(policiesQuery.data ?? []).map((row) => ({ label: row.name, value: row.id })),
  ], [policiesQuery.data, t]);

  if (enforcedQuery.isError) {
    return (
      <Alert
        type="warning"
        showIcon
        message={t('pages.policies.readbackFailed')}
        action={(
          <Button
            size="small"
            icon={<ReloadOutlined />}
            loading={enforcedQuery.isFetching}
            onClick={() => { void enforcedQuery.refetch(); }}
          >
            {t('refresh')}
          </Button>
        )}
      />
    );
  }

  const loading = value === POLICY_UNLOADED;

  return (
    <Space direction="vertical" size={8} style={{ width: '100%' }}>
      <Form.Item label={t('pages.policies.plan')} tooltip={t('pages.policies.planHint')} style={{ marginBottom: 0 }}>
        <Select
          value={loading ? undefined : value}
          loading={loading || policiesQuery.isPending}
          disabled={loading}
          options={options}
          placeholder={t('pages.policies.noPlan')}
          onChange={(next) => onSelect(Number(next) || POLICY_NONE)}
        />
      </Form.Item>

      {enforced?.unresolved && (
        <Alert type="warning" showIcon message={t('pages.policies.unresolved')} />
      )}

      {kinds.length > 0 && verdict.shapeable.length === 0 && (
        <Alert
          type="info"
          showIcon
          message={t('pages.policies.notShapeable')}
          description={<KindTags kinds={verdict.unshapeable} />}
        />
      )}

      {verdict.shapeable.length > 0 && verdict.unshapeable.length > 0 && (
        <Alert
          type="info"
          showIcon
          message={t('pages.policies.partlyShapeable')}
          description={<KindTags kinds={verdict.shapeable} />}
        />
      )}

      {shapedInbounds > 1 && (
        <Alert type="info" showIcon message={t('pages.policies.perInbound')} />
      )}

      {enforced && (
        <Descriptions size="small" column={1} bordered items={[
          {
            key: 'used',
            label: t('pages.policies.used'),
            children: <span dir="ltr">{SizeFormatter.sizeFormat(enforced.usedBytes)}</span>,
          },
          {
            key: 'want',
            label: t('pages.policies.inEffect'),
            children: (
              <span dir="ltr">
                ↓ {formatRate(enforced.wantDownBps, t)} / ↑ {formatRate(enforced.wantUpBps, t)}
              </span>
            ),
          },
          {
            key: 'enforced',
            label: t('pages.policies.enforced'),
            children: enforced.shapeable ? (
              <span dir="ltr">
                ↓ {formatRate(enforced.enforcedDownBps, t)} / ↑ {formatRate(enforced.enforcedUpBps, t)}
              </span>
            ) : (
              <Typography.Text type="secondary">{t('pages.policies.notEnforcedYet')}</Typography.Text>
            ),
          },
        ]} />
      )}
    </Space>
  );
}
