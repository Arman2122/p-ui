import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Space, Table, Typography } from 'antd';
import { PlusOutlined } from '@ant-design/icons';

import { useRoutingMutations } from '@/api/queries/useRoutingMutations';
import { useRoutingRulesQuery, useRoutingSubjectsQuery } from '@/api/queries/useRoutingQuery';
import {
  criteriaToForm,
  ingressIdsToArray,
  type RoutingRulePayload,
  type RoutingRuleRecord,
  type RoutingSubjectView,
} from '@/schemas/api/routing';

import RuleEditorModal from './RuleEditorModal';
import { useRoutingColumns } from './useRoutingColumns';
import type { InboundTagOption, RuleRow } from './types';

interface RoutingRulesPanelProps {
  isMobile: boolean;
  outboundTags: string[];
  balancerTags: string[];
}

/*
The operator's own rules, rendered by the SAME table the template rules use.

Deliberately not a second list with a look of its own: the two-screen split is
what this work exists to remove, and useRoutingColumns already takes its
handlers as parameters, so the same columns serve both with different writers.
*/
export default function RoutingRulesPanel({
  isMobile, outboundTags, balancerTags,
}: RoutingRulesPanelProps) {
  const { t } = useTranslation();
  const { data: records = [], isLoading } = useRoutingRulesQuery();
  const { data: subjects = [] } = useRoutingSubjectsQuery();
  const { add, update, remove, reorder, saving } = useRoutingMutations();
  const [editing, setEditing] = useState<RoutingRuleRecord | null>(null);
  const [open, setOpen] = useState(false);

  const tagById = useMemo(() => {
    const map = new Map<number, RoutingSubjectView>();
    for (const subject of subjects) map.set(subject.inboundId, subject);
    return map;
  }, [subjects]);

  /* Intent expressed in the row shape the shared columns already render, so a
     rule reads the same whichever half of the router carries it. */
  const rows: RuleRow[] = useMemo(
    () => records.map((record, index) => {
      const criteria = criteriaToForm(record.criteria);
      const tags = record.ingressScope === 'all'
        ? t('pages.xray.routing.scopeAll')
        : ingressIdsToArray(record.ingressIds)
          .map((id) => tagById.get(id)?.tag ?? t('pages.xray.routing.unknownInbound', { id }))
          .join(',');
      return {
        key: index,
        enabled: record.enable,
        inboundTag: tags,
        domain: criteria.domain,
        ip: criteria.ip,
        port: criteria.port,
        sourcePort: criteria.sourcePort,
        network: criteria.network,
        sourceIP: criteria.source,
        protocol: criteria.protocol,
        user: criteria.user,
        outboundTag: record.destKind === 'balancer' ? undefined : destLabel(record, t),
        balancerTag: record.destKind === 'balancer' ? record.destTag : undefined,
      };
    }),
    [records, tagById, t],
  );

  /* The shared columns badge a rule whose inbound the router cannot see. For
     intent rules that answer comes from the SUBJECTS, not from the template's
     tag list: a kernel inbound is unroutable there and routable here, because
     this is the half that fronts it. */
  const inboundTagOptions: InboundTagOption[] = useMemo(
    () => subjects.map((subject) => ({
      value: subject.tag,
      disabled: !subject.routable,
      reasonKey: subject.blockedKey || undefined,
    })),
    [subjects],
  );

  const move = async (index: number, delta: number) => {
    const next = [...records];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    await reorder(next.map((record) => record.id));
  };

  const columns = useRoutingColumns({
    isMobile,
    inboundTagOptions,
    rowsLength: rows.length,
    showSource: rows.some((row) => row.sourceIP || row.sourcePort),
    showBalancer: rows.some((row) => row.balancerTag),
    onHandlePointerDown: () => {},
    openEdit: (index) => { setEditing(records[index] ?? null); setOpen(true); },
    moveUp: (index) => void move(index, -1),
    moveDown: (index) => void move(index, 1),
    confirmDelete: (index) => { const row = records[index]; if (row) void remove(row.id); },
    toggleRule: (index, enabled) => {
      const row = records[index];
      if (row) void update(row.id, { ...toPayload(row), enable: enabled });
    },
  });

  const save = async (payload: RoutingRulePayload) => {
    if (editing) await update(editing.id, payload);
    else await add(payload);
    setOpen(false);
    setEditing(null);
  };

  const blocked = subjects.filter((subject) => !subject.routable);

  return (
    <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
      <Space wrap>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditing(null); setOpen(true); }}>
          {t('pages.xray.routing.addRule')}
        </Button>
        <Typography.Text type="secondary">{t('pages.xray.routing.intentHint')}</Typography.Text>
      </Space>

      {blocked.length > 0 && (
        <Alert
          type="info"
          showIcon
          message={t('pages.xray.routing.someInboundsUnroutable', { count: blocked.length })}
          description={
            <ul style={{ margin: 0, paddingInlineStart: 18 }}>
              {blocked.map((subject) => (
                <li key={subject.inboundId}>{subject.tag}: {t(subject.blockedKey)}</li>
              ))}
            </ul>
          }
        />
      )}

      <Table
        columns={columns}
        dataSource={rows}
        rowKey={(row) => row.key}
        loading={isLoading}
        pagination={false}
        size="small"
        locale={{ emptyText: t('pages.xray.routing.noRules') }}
      />

      <RuleEditorModal
        open={open}
        rule={editing}
        subjects={subjects}
        outboundTags={outboundTags}
        balancerTags={balancerTags}
        saving={saving}
        onClose={() => { setOpen(false); setEditing(null); }}
        onSave={(payload) => void save(payload)}
      />
    </Space>
  );
}

function destLabel(record: RoutingRuleRecord, t: (key: string) => string): string {
  if (record.destKind === 'direct') return t('pages.xray.routing.dest.direct');
  if (record.destKind === 'block') return t('pages.xray.routing.dest.block');
  return record.destTag;
}

// toPayload round-trips a stored rule so a toggle does not rewrite its shape.
function toPayload(record: RoutingRuleRecord): RoutingRulePayload {
  return {
    enable: record.enable,
    remark: record.remark ?? '',
    ingressScope: record.ingressScope,
    ingressIds: JSON.stringify(ingressIdsToArray(record.ingressIds)),
    destKind: record.destKind,
    destTag: record.destTag ?? '',
    destExitId: record.destExitId ?? null,
    criteria: typeof record.criteria === 'string' ? record.criteria : JSON.stringify(record.criteria),
    inspect: record.inspect ?? false,
  };
}
