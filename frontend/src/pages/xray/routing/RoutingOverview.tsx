import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Empty, Space, Switch, Tag, Tooltip } from 'antd';
import { ArrowDownOutlined, ArrowRightOutlined, ArrowUpOutlined, CopyOutlined, DeleteOutlined, EditOutlined, HolderOutlined, WarningOutlined } from '@ant-design/icons';
import { PanelCard } from '@/components/ui/panel';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import { buildRemarkByTag, canSwapRows, inboundTagsDisplayTitle, isApiRule, ruleCriteriaChips } from './helpers';
import type { RuleRow } from './types';

interface RoutingOverviewProps {
  rows: RuleRow[];
  blockingTags: Set<string>;
  exitLabels: Map<string, string>;
  draggedIndex: number | null;
  dropTargetIndex: number | null;
  onHandlePointerDown: (index: number, event: React.PointerEvent) => void;
  openEdit: (index: number) => void;
  duplicateRule: (index: number) => void;
  confirmDelete: (index: number) => void;
  toggleRule: (index: number, enabled: boolean) => void;
  moveUp: (index: number) => void;
  moveDown: (index: number) => void;
}

export default function RoutingOverview({ rows, blockingTags, exitLabels, draggedIndex, dropTargetIndex, onHandlePointerDown, openEdit, duplicateRule, confirmDelete, toggleRule, moveUp, moveDown }: RoutingOverviewProps) {
  const { t } = useTranslation();
  const [reordering, setReordering] = useState(false);
  const { data: inbounds = [] } = useInboundOptions();
  const remarks = useMemo(() => buildRemarkByTag(inbounds), [inbounds]);
  const active = rows.filter((row) => row.enabled !== false).length;
  const blocked = rows.filter((row) => row.destKind === 'block' || blockingTags.has(row.outboundTag ?? '')).length;
  const destinations = new Set(rows.filter((row) => !isApiRule(row)).map((row) => row.balancerTag ? `balancer:${row.balancerTag}` : row.outboundTag).filter(Boolean)).size;
  const canReorder = rows.some((row, index) => canSwapRows(row, rows[index + 1]));

  return <div className="routing-overview">
    <div className="routing-overview-stats">
      <PanelCard kicker={t('routingPage.rules')} pad={14}><div className="p-stat">{rows.length}</div><div className="p-sub">{t('routingPage.activeCount', { count: active })}</div></PanelCard>
      <PanelCard kicker={t('routingPage.blockingRules')} pad={14}><div className="p-stat">{blocked}</div><div className="p-sub">{t('routingPage.blockingHint')}</div></PanelCard>
      <PanelCard kicker={t('routingPage.destinations')} pad={14}><div className="p-stat">{destinations}</div><div className="p-sub">{t('routingPage.destinationsHint')}</div></PanelCard>
    </div>
    <PanelCard pad={0} className="routing-overview-panel">
      <div className="routing-overview-toolbar">
        <div><strong>{t('routingPage.firstMatch')}</strong><div className="p-sub">{t('routingPage.draftHint')}</div></div>
        <Button size="small" icon={<HolderOutlined />} disabled={!canReorder} aria-pressed={reordering} onClick={() => setReordering((value) => !value)}>{t(reordering ? 'routingPage.finishReorder' : 'routingPage.reorder')}</Button>
      </div>
      <p className="routing-overview-order-note">{t('routingPage.orderBoundary')}</p>
      {rows.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('routingPage.empty')} />}
      <ol className="routing-overview-list" aria-label={t('routingPage.rules')}>
        {rows.map((row, index) => {
          const fixed = isApiRule(row);
          const name = row.remark || t('routingPage.ruleNumber', { number: index + 1 });
          const criteria = ruleCriteriaChips(row);
          const destination = row.balancerTag || exitLabels.get(row.outboundTag ?? '') || row.outboundTag || '—';
          const firstOfSource = index === 0 || rows[index - 1].source !== row.source;
          const movable = canSwapRows(row, rows[index - 1]) || canSwapRows(row, rows[index + 1]);
          return <li key={`${row.source}-${row.key}`} className={`routing-overview-item${firstOfSource ? ' routing-overview-source-start' : ''}`}>
            {firstOfSource && <div className="routing-overview-source">{t(row.source === 'intent' ? 'routingPage.operatorRules' : 'routingPage.templateRules')}</div>}
            <div data-row-key={index} className={`routing-overview-row${row.enabled === false ? ' is-disabled' : ''}${draggedIndex === index ? ' row-dragging' : ''}${dropTargetIndex === index && draggedIndex !== null && draggedIndex !== index ? index < draggedIndex ? ' drop-before' : ' drop-after' : ''}`}>
              <span className="routing-overview-grip" aria-hidden="true">{movable && <HolderOutlined className="drag-handle" onPointerDown={(event) => onHandlePointerDown(index, event)} />}</span>
              <span className="routing-overview-rank">{index + 1}</span>
              <Switch size="small" checked={row.enabled !== false} disabled={fixed} aria-label={`${t('enable')} ${name}`} onChange={(enabled) => toggleRule(index, enabled)} />
              <div className="routing-overview-match">
                {row.remark && <strong>{row.remark}</strong>}
                <div className="routing-overview-criteria">
                  {criteria.length ? criteria.slice(0, 2).map((criterion) => <span className="routing-overview-criterion" key={criterion.label}>
                    <Tag>{criterion.label}</Tag><span className="p-mono" title={criterion.value} dir="ltr">{criterion.value}</span>
                  </span>) : <span>{t('routingPage.matchAll')}</span>}
                  {criteria.length > 2 && <Tooltip title={criteria.slice(2).map((item) => `${item.label}: ${item.value}`).join(' · ')}><Tag>+{criteria.length - 2}</Tag></Tooltip>}
                </div>
                <span className="routing-overview-scope">{row.inboundTag ? inboundTagsDisplayTitle(row.inboundTag, remarks) : t('pages.xray.routing.scopeAll')}</span>
              </div>
              <ArrowRightOutlined className="routing-overview-arrow" aria-hidden="true" />
              <Tag className="routing-overview-destination"><span dir="ltr">{destination}</span></Tag>
              {!!row.diags?.length && <Tooltip title={row.diags.map((diag) => t(diag.messageKey, diag.args)).join(' · ')}><span role="img" aria-label={t('pages.xray.routing.inertTitle')}><WarningOutlined /></span></Tooltip>}
              <Space size={2} className="routing-overview-actions">
                {reordering && <>
                  <Button type="text" size="small" icon={<ArrowUpOutlined />} disabled={!canSwapRows(row, rows[index - 1])} aria-label={`${t('pages.inbounds.form.moveUp')} ${name}`} onClick={() => moveUp(index)} />
                  <Button type="text" size="small" icon={<ArrowDownOutlined />} disabled={!canSwapRows(row, rows[index + 1])} aria-label={`${t('pages.inbounds.form.moveDown')} ${name}`} onClick={() => moveDown(index)} />
                </>}
                <Tooltip title={fixed ? t('routingPage.fixedRule') : t('edit')}><Button type="text" size="small" icon={<EditOutlined />} disabled={fixed} aria-label={`${t('edit')} ${name}`} onClick={() => openEdit(index)} /></Tooltip>
                <Tooltip title={fixed ? t('routingPage.fixedRule') : t('routingPage.duplicate')}><Button type="text" size="small" icon={<CopyOutlined />} disabled={fixed} aria-label={`${t('routingPage.duplicate')} ${name}`} onClick={() => duplicateRule(index)} /></Tooltip>
                <Tooltip title={fixed ? t('routingPage.fixedRule') : t('delete')}><Button type="text" size="small" danger icon={<DeleteOutlined />} disabled={fixed} aria-label={`${t('delete')} ${name}`} onClick={() => confirmDelete(index)} /></Tooltip>
              </Space>
            </div>
          </li>;
        })}
      </ol>
    </PanelCard>
  </div>;
}
