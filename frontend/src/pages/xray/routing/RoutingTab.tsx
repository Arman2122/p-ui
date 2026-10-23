import { useCallback, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Dropdown, Modal, Skeleton, Space, Table, Tabs, message } from 'antd';
import {
  AimOutlined,
  ControlOutlined,
  ExportOutlined,
  ImportOutlined,
  MoreOutlined,
  PlusOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';

import { catTabLabel } from '@/pages/settings/catTabLabel';
import PromptModal from '@/components/feedback/PromptModal';
import TextModal from '@/components/feedback/TextModal';
import { isBalancerLoopbackTag } from '../balancers/balancer-loopback';
import RoutingBasic from './RoutingBasic';
import RouteTester from './RouteTester';
import RuleFormModal from './RuleFormModal';
import type { RoutingRule } from './RuleFormModal';
import RuleCardList from './RuleCardList';
import RoutingOverview from './RoutingOverview';
import { useRoutingColumns } from './useRoutingColumns';
import { arrJoin, buildInboundTagOptions, canSwapRows, exportableRuleList, originalRuleIndex } from './helpers';
import type { RoutingDraft } from '@/hooks/useRoutingDraft';
import {
  useRoutingExitsQuery,
  useRoutingSubjectsQuery,
} from '@/api/queries/useRoutingQuery';
import { intentToRule, recordToPayload, ruleToIntent, type XrayRuleShape } from '@/schemas/api/routing';
import type { RoutingSubject, RuleRow } from './types';
import type { XraySettingsValue, SetTemplate } from '@/hooks/useXraySetting';
import type { RuleObject } from '@/schemas/routing';
import './RoutingTab.css';

interface RoutingTabProps {
  templateSettings: XraySettingsValue | null;
  setTemplateSettings: SetTemplate;
  inboundTags: string[];
  routingSubjects?: RoutingSubject[];
  clientReverseTags: string[];
  subscriptionOutboundTags?: string[];
  isMobile: boolean;
  /* Owned by the page so its Save button can see the changes and flush them. */
  routing: RoutingDraft;
  standalone?: boolean;
  renderHeader?: (actions: ReactNode) => ReactNode;
  hasUnsavedChanges?: boolean;
}

export default function RoutingTab({
  templateSettings,
  setTemplateSettings,
  inboundTags,
  routingSubjects,
  clientReverseTags,
  subscriptionOutboundTags,
  isMobile,
  routing,
  standalone = false,
  renderHeader,
  hasUnsavedChanges = false,
}: RoutingTabProps) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [ruleModalOpen, setRuleModalOpen] = useState(false);
  const [editingRule, setEditingRule] = useState<RoutingRule | null>(null);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [editingSource, setEditingSource] = useState<'intent' | 'template'>('intent');
  const [draggedIndex, setDraggedIndex] = useState<number | null>(null);
  const [dropTargetIndex, setDropTargetIndex] = useState<number | null>(null);
  const rulesUnavailable = !!routing.loading || !!routing.error;
  const dragRef = useRef<{ from: number | null; to: number | null; startY: number; moved: boolean }>({
    from: null,
    to: null,
    startY: 0,
    moved: false,
  });

  const rules = useMemo(
    () => (templateSettings?.routing?.rules || []) as RoutingRule[],
    [templateSettings?.routing?.rules],
  );
  const rulesRef = useRef(rules);
  rulesRef.current = rules;
  const rowsRef = useRef<RuleRow[]>([]);

  const rows: RuleRow[] = useMemo(
    () =>
      rules
        .map((rule, idx) => {
          const r: RuleRow = { key: idx };
          r.enabled = rule.enabled !== false;
          r.remark = typeof rule.remark === 'string' ? rule.remark : undefined;
          r.domain = arrJoin(rule.domain);
          r.ip = arrJoin(rule.ip);
          r.port = rule.port;
          r.sourcePort = rule.sourcePort;
          r.vlessRoute = rule.vlessRoute;
          r.network = rule.network;
          r.sourceIP = arrJoin(rule.sourceIP);
          r.localIP = arrJoin(rule.localIP);
          r.localPort = arrJoin(rule.localPort);
          r.process = arrJoin(rule.process);
          r.user = arrJoin(rule.user);
          r.inboundTag = arrJoin(rule.inboundTag);
          r.protocol = arrJoin(rule.protocol);
          if (rule.attrs && typeof rule.attrs === 'object' && !Array.isArray(rule.attrs)) {
            r.attrs = JSON.stringify(rule.attrs, null, 2);
          }
          r.outboundTag = rule.outboundTag;
          r.balancerTag = rule.balancerTag;
          return r;
        })
        .filter((r) => {
          const inboundTags = (rules[r.key]?.inboundTag || []) as string[];
          return !inboundTags.some(isBalancerLoopbackTag);
        }),
    [rules],
  );
  rowsRef.current = rows;

  const mutate = useCallback(
    (mutator: (next: XraySettingsValue) => void) => {
      if (rulesUnavailable) return;
      setTemplateSettings((prev) => {
        if (!prev) return prev;
        const clone = JSON.parse(JSON.stringify(prev)) as XraySettingsValue;
        mutator(clone);
        return clone;
      });
    },
    [setTemplateSettings, rulesUnavailable],
  );

  const inboundTagOptions = useMemo(() => {
    const seen = new Set<string>();
    const templateTags: string[] = [];
    const push = (tag?: string) => {
      if (!tag || seen.has(tag) || isBalancerLoopbackTag(tag)) return;
      seen.add(tag);
      templateTags.push(tag);
    };
    for (const ib of (templateSettings?.inbounds as Array<{ tag?: string }>) || []) push(ib?.tag);
    for (const tag of inboundTags || []) push(tag);
    for (const ob of templateSettings?.outbounds || []) {
      const obx = ob as { reverse?: { tag?: string }; settings?: { reverse?: { tag?: string }; inboundTag?: string } };
      push(obx?.reverse?.tag || obx?.settings?.reverse?.tag || obx?.settings?.inboundTag);
    }
    push((templateSettings?.dns as { tag?: string } | undefined)?.tag);
    for (const s of (templateSettings?.dns as { servers?: Array<{ tag?: string }> } | undefined)?.servers || []) {
      if (typeof s === 'object' && s?.tag) push(s.tag);
    }
    return buildInboundTagOptions(routingSubjects, templateTags);
  }, [templateSettings, inboundTags, routingSubjects]);

  const intentRecords = routing.rules;
  const { data: subjects = [] } = useRoutingSubjectsQuery();
  const { data: exits = [] } = useRoutingExitsQuery();

  const tagOfInbound = useMemo(() => {
    const map = new Map<number, string>();
    for (const subject of subjects) map.set(subject.inboundId, subject.tag);
    return map;
  }, [subjects]);
  const idOfTag = useMemo(() => {
    const map = new Map<string, number>();
    for (const subject of subjects) map.set(subject.tag, subject.inboundId);
    return map;
  }, [subjects]);

  /*
  One list, in the order Xray actually evaluates it: the operator's own rules
  compile above the template's, so they are shown above them. Which store a row
  lives in is the panel's problem, and `source` is the only place it shows.
  */
  const intentRows: RuleRow[] = useMemo(
    () => intentRecords.map((record, index) => {
      const shape = intentToRule(record, (id) => tagOfInbound.get(id));
      return {
        key: 10000 + index,
        source: 'intent' as const,
        storeIndex: index,
        enabled: record.enable,
        remark: record.remark,
        destKind: record.destKind,
        inboundTag: (shape.inboundTag ?? []).join(',') || t('pages.xray.routing.scopeAll'),
        domain: arrJoin(shape.domain),
        ip: arrJoin(shape.ip),
        port: shape.port,
        sourcePort: shape.sourcePort,
        network: shape.network,
        sourceIP: arrJoin(shape.sourceIP),
        user: arrJoin(shape.user),
        protocol: arrJoin(shape.protocol),
        attrs: shape.attrs ? JSON.stringify(shape.attrs) : undefined,
        outboundTag: shape.outboundTag,
        balancerTag: shape.balancerTag,
        diags: record.diags,
      };
    }),
    [intentRecords, tagOfInbound, t],
  );

  const mergedRows: RuleRow[] = useMemo(
    () => [
      ...intentRows,
      ...rows.map((row) => ({ ...row, source: 'template' as const, storeIndex: row.key })),
    ],
    [intentRows, rows],
  );

  const mergedRowsRef = useRef<RuleRow[]>([]);
  mergedRowsRef.current = mergedRows;
  const intentRecordsRef = useRef(intentRecords);
  intentRecordsRef.current = intentRecords;
  const tagOfInboundRef = useRef(tagOfInbound);
  tagOfInboundRef.current = tagOfInbound;
  const idOfTagRef = useRef(idOfTag);
  idOfTagRef.current = idOfTag;

  // Reordering an operator rule writes the whole order in one call, so a failure
  // cannot leave first-match half applied.
  function moveIntent(index: number, delta: number) {
    const next = [...intentRecordsRef.current];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    routing.reorderRules(next);
  }

  const outboundTagOptions = useMemo(() => {
    const out = new Set<string>(['']);
    for (const ob of templateSettings?.outbounds || []) {
      if (ob?.tag) out.add(ob.tag);
    }
    for (const tag of clientReverseTags || []) {
      if (tag) out.add(tag);
    }
    for (const tag of subscriptionOutboundTags || []) {
      if (tag) out.add(tag);
    }
    return [...out];
  }, [templateSettings?.outbounds, clientReverseTags, subscriptionOutboundTags]);

  const balancerTagOptions = useMemo(() => {
    const out: string[] = [''];
    for (const b of (templateSettings?.routing?.balancers as Array<{ tag?: string }>) || []) {
      if (b?.tag) out.push(b.tag);
    }
    return out;
  }, [templateSettings?.routing?.balancers]);

  const [importOpen, setImportOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [exportContent, setExportContent] = useState('');

  /* Everything the table shows, in the order it shows it. Exporting `rules`
     alone silently dropped every rule the operator created here -- the dialog
     writes those to the panel's own store, not into the template. F192. */
  const exportableRules = useMemo(
    () => exportableRuleList(intentRecords, rules as XrayRuleShape[], (id) => tagOfInbound.get(id)),
    [intentRecords, tagOfInbound, rules],
  );

  function exportRules() {
    setExportContent(JSON.stringify(exportableRules, null, 2));
    setExportOpen(true);
  }

  function importRules(value: string) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(value);
    } catch {
      message.error(t('pages.xray.importInvalidJson'));
      return;
    }
    const obj = parsed as { rules?: unknown; routing?: { rules?: unknown } };
    const list = Array.isArray(parsed)
      ? parsed
      : Array.isArray(obj?.rules)
        ? obj.rules
        : Array.isArray(obj?.routing?.rules)
          ? obj.routing!.rules
          : null;
    if (!list) {
      message.error(t('pages.xray.importInvalidJson'));
      return;
    }
    mutate((tt) => {
      if (!tt.routing) tt.routing = { rules: [] };
      if (!Array.isArray(tt.routing.rules)) tt.routing.rules = [];
      tt.routing.rules.push(...(list as RuleObject[]));
    });
    setImportOpen(false);
  }

  /* A row knows which store it came from, so one set of handlers serves both.
     Adding always creates an OPERATOR rule: the template half is what the panel
     maintains, and new intent belongs where the panel can compile it. */
  const rowAt = (idx: number) => mergedRowsRef.current[idx];

  function openAdd() {
    setEditingRule(null);
    setEditingIndex(null);
    setEditingSource('intent');
    setRuleModalOpen(true);
  }
  function openEdit(idx: number) {
    const row = rowAt(idx);
    if (row?.source === 'intent') {
      const record = intentRecordsRef.current[row.storeIndex ?? 0];
      setEditingRule(record ? intentToRule(record, (id) => tagOfInboundRef.current.get(id)) : null);
      setEditingIndex(row.storeIndex ?? null);
      setEditingSource('intent');
      setRuleModalOpen(true);
      return;
    }
    const target = row?.storeIndex ?? originalRuleIndex(rowsRef.current, idx);
    setEditingRule(rulesRef.current[target]);
    setEditingIndex(target);
    setEditingSource('template');
    setRuleModalOpen(true);
  }
  function onRuleConfirm(rule: Record<string, unknown>) {
    if (rulesUnavailable) return;
    if (JSON.stringify(rule).length <= 3) {
      setRuleModalOpen(false);
      return;
    }
    if (editingSource === 'intent') {
      const result = ruleToIntent(rule as XrayRuleShape, (tag) => idOfTagRef.current.get(tag));
      if (!result.ok) {
        // The modal stays open on purpose: the operator's edits are still in it.
        message.error(t('pages.xray.routing.unresolvedInbound', { tags: result.unresolved.join(', ') }));
        return;
      }
      const record = editingIndex == null ? null : intentRecordsRef.current[editingIndex];
      if (record) routing.updateRule(record.id, result.payload);
      else routing.addRule(result.payload);
      setRuleModalOpen(false);
      return;
    }
    mutate((tt) => {
      if (!tt.routing) tt.routing = { rules: [] };
      if (!Array.isArray(tt.routing.rules)) tt.routing.rules = [];
      const typed = rule as unknown as RuleObject;
      if (editingIndex == null) tt.routing.rules.push(typed);
      else tt.routing.rules[editingIndex] = typed;
    });
    setRuleModalOpen(false);
  }

  function confirmDelete(idx: number) {
    const row = rowAt(idx);
    /* Both halves ask. An operator rule used to go on the first click while a
       template rule asked — the same gesture on two adjacent rows, one of them
       undoable only by retyping it. */
    const remove = row?.source === 'intent'
      ? () => {
        const record = intentRecordsRef.current[row.storeIndex ?? 0];
        if (record) routing.removeRule(record.id);
      }
      : () => {
        const target = row?.storeIndex ?? originalRuleIndex(rowsRef.current, idx);
        mutate((tt) => {
          tt.routing?.rules?.splice(target, 1);
        });
      };
    modal.confirm({
      title: `${t('delete')} ${t('pages.xray.Routings')} #${idx + 1}?`,
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: remove,
    });
  }

  function duplicateRule(idx: number) {
    const row = rowAt(idx);
    if (row?.source === 'intent') {
      const record = intentRecordsRef.current[row.storeIndex ?? 0];
      if (record) routing.addRule(recordToPayload(record));
      return;
    }
    const target = row?.storeIndex ?? originalRuleIndex(rowsRef.current, idx);
    mutate((next) => {
      const list = next.routing?.rules;
      if (list?.[target]) list.splice(target + 1, 0, JSON.parse(JSON.stringify(list[target])) as RuleObject);
    });
  }

  function moveUp(idx: number) {
    if (idx <= 0) return;
    const row = rowAt(idx);
    const prevRow = rowAt(idx - 1);
    if (!canSwapRows(row, prevRow)) return;
    if (row.source === 'intent') { void moveIntent(row.storeIndex ?? 0, -1); return; }
    const target = row.storeIndex ?? originalRuleIndex(rowsRef.current, idx);
    const prev = prevRow.storeIndex;
    if (prev == null) return;
    mutate((tt) => {
      const list = tt.routing?.rules;
      if (!list || !list[target] || !list[prev]) return;
      [list[prev], list[target]] = [list[target], list[prev]];
    });
  }
  function moveDown(idx: number) {
    if (idx >= mergedRowsRef.current.length - 1) return;
    const row = rowAt(idx);
    const nextRow = rowAt(idx + 1);
    if (!canSwapRows(row, nextRow)) return;
    if (row.source === 'intent') { void moveIntent(row.storeIndex ?? 0, 1); return; }
    const target = row.storeIndex ?? originalRuleIndex(rowsRef.current, idx);
    const next = nextRow.storeIndex;
    if (next == null) return;
    mutate((tt) => {
      const list = tt.routing?.rules;
      if (!list || !list[target] || !list[next]) return;
      [list[next], list[target]] = [list[target], list[next]];
    });
  }
  function toggleRule(idx: number, enabled: boolean) {
    const row = rowAt(idx);
    if (row?.source === 'intent') {
      const record = intentRecordsRef.current[row.storeIndex ?? 0];
      /* Straight from the stored row. Round-tripping through the editor's shape
         recomputed scope, criteria and destination from a toggle of one flag. */
      if (record) routing.updateRule(record.id, { ...recordToPayload(record), enable: enabled });
      return;
    }
    const target = row?.storeIndex ?? originalRuleIndex(rowsRef.current, idx);
    mutate((tt) => {
      const list = tt.routing?.rules;
      if (!list || !list[target]) return;
      list[target].enabled = enabled;
    });
  }

  function onHandlePointerDown(idx: number, ev: React.PointerEvent) {
    if (ev.button != null && ev.button !== 0) return;
    ev.preventDefault();
    try {
      (ev.currentTarget as Element).setPointerCapture(ev.pointerId);
    } catch { /* ignore */ }
    dragRef.current = { from: idx, to: idx, startY: ev.clientY, moved: false };
    setDraggedIndex(idx);
    setDropTargetIndex(idx);

    const onMove = (e: PointerEvent) => {
      const state = dragRef.current;
      if (state.from == null) return;
      if (!state.moved && Math.abs(e.clientY - state.startY) < 5) return;
      state.moved = true;
      const el = document.elementFromPoint(e.clientX, e.clientY);
      if (!el) return;
      const target = el.closest('[data-row-key]');
      if (!target) return;
      const newIdx = Number(target.getAttribute('data-row-key'));
      if (Number.isFinite(newIdx) && newIdx !== state.to) {
        state.to = newIdx;
        setDropTargetIndex(newIdx);
      }
    };

    const onUp = () => {
      document.removeEventListener('pointermove', onMove);
      document.removeEventListener('pointerup', onUp);
      document.removeEventListener('pointercancel', onUp);
      const { from, to, moved } = dragRef.current;
      dragRef.current = { from: null, to: null, startY: 0, moved: false };
      setDraggedIndex(null);
      setDropTargetIndex(null);
      if (!moved || from == null || to == null || from === to) return;
      const fromRow = mergedRowsRef.current[from];
      const toRow = mergedRowsRef.current[to];
      /* The two lists are ordered independently and the table merges them into
         evaluation order, so a drag across the boundary names no position in
         either. Resolving a MERGED index against the template list is what used
         to reorder a template rule when an operator rule was dragged. */
      if (!canSwapRows(fromRow, toRow)) return;
      if (fromRow.source === 'intent') {
        const next = [...intentRecordsRef.current];
        const [movedRecord] = next.splice(fromRow.storeIndex ?? 0, 1);
        next.splice(toRow.storeIndex ?? 0, 0, movedRecord);
        routing.reorderRules(next);
        return;
      }
      const fromOrig = fromRow.storeIndex ?? originalRuleIndex(rowsRef.current, from);
      const toOrig = toRow.storeIndex ?? originalRuleIndex(rowsRef.current, to);
      mutate((tt) => {
        const list = tt.routing?.rules;
        if (!list) return;
        const [movedItem] = list.splice(fromOrig, 1);
        list.splice(toOrig, 0, movedItem);
      });
    };

    document.addEventListener('pointermove', onMove);
    document.addEventListener('pointerup', onUp);
    document.addEventListener('pointercancel', onUp);
  }

  const hasSource = mergedRows.some((r) => r.sourceIP || r.sourcePort || r.vlessRoute);
  const hasBalancer = mergedRows.some((r) => r.balancerTag);

  const desktopColumns = useRoutingColumns({
    isMobile,
    inboundTagOptions,
    rows: mergedRows,
    showSource: hasSource,
    showBalancer: hasBalancer,
    onHandlePointerDown,
    openEdit,
    duplicateRule,
    moveUp,
    moveDown,
    confirmDelete,
    toggleRule,
  });

  const tableScrollX = desktopColumns.reduce((sum, c) => {
    const col = c as { width?: number; hidden?: boolean };
    return col.hidden ? sum : sum + (typeof col.width === 'number' ? col.width : 0);
  }, 0);

  const ruleActions = <Space wrap>
    <Button type="primary" icon={<PlusOutlined />} disabled={rulesUnavailable} onClick={openAdd}>{t('routingPage.addRule')}</Button>
    <Dropdown trigger={['click']} menu={{ items: [
      { key: 'import', icon: <ImportOutlined />, label: t('pages.xray.importRules'), disabled: rulesUnavailable, onClick: () => setImportOpen(true) },
      { key: 'export', icon: <ExportOutlined />, label: t('pages.xray.exportRules'), disabled: rulesUnavailable || exportableRules.length === 0, onClick: exportRules },
    ] }}><Button icon={<MoreOutlined />}>{t('more')}</Button></Dropdown>
  </Space>;
  const blockingTags = new Set(['blocked', ...(templateSettings?.outbounds ?? []).filter((outbound) => outbound.protocol?.toLowerCase() === 'blackhole').map((outbound) => outbound.tag ?? '')]);
  const exitLabels = new Map(exits.map((exit) => [`exit:${exit.id}`, exit.label]));

  return (
    <>
      {modalContextHolder}
      {standalone && (renderHeader ? renderHeader(ruleActions) : <div className="routing-standalone-actions">{ruleActions}</div>)}
      {routing.error && <Alert className="routing-load-error" type="error" showIcon title={t('somethingWentWrong')} description={routing.error.message}
        action={<Button disabled={!routing.retry} onClick={routing.retry}>{t('routingPage.retry')}</Button>} />}
      <div inert={rulesUnavailable || undefined} aria-busy={routing.loading || undefined}>
      <Tabs
        key={standalone ? 'standalone' : 'embedded'}
        defaultActiveKey={standalone ? 'overview' : 'basic'}
        className={standalone ? 'routing-standalone' : undefined}
        items={[
          ...(standalone ? [{
            key: 'overview',
            label: catTabLabel(<UnorderedListOutlined />, t('routingPage.overview'), isMobile),
            children: rulesUnavailable ? <Skeleton active={routing.loading} /> : <RoutingOverview rows={mergedRows} blockingTags={blockingTags} exitLabels={exitLabels}
              draggedIndex={draggedIndex} dropTargetIndex={dropTargetIndex} onHandlePointerDown={onHandlePointerDown}
              openEdit={openEdit} duplicateRule={duplicateRule} confirmDelete={confirmDelete} toggleRule={toggleRule} moveUp={moveUp} moveDown={moveDown} />,
          }] : []),
          {
            key: 'basic',
            label: catTabLabel(<ControlOutlined />, t('pages.xray.basicRouting'), isMobile),
            children: (
              <div className={standalone ? 'routing-settings-panel' : undefined}><RoutingBasic
                templateSettings={templateSettings}
                setTemplateSettings={setTemplateSettings}
              /></div>
            ),
          },
          {
            key: 'rules',
            label: catTabLabel(<UnorderedListOutlined />, t(standalone ? 'routingPage.details' : 'pages.xray.Routings'), isMobile),
            children: (
              <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
                {!standalone && <Space wrap>
                  <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>
                    {t('pages.xray.Routings')}
                  </Button>
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: [
                        { key: 'import', icon: <ImportOutlined />, label: t('pages.xray.importRules'), onClick: () => setImportOpen(true) },
                        { key: 'export', icon: <ExportOutlined />, label: t('pages.xray.exportRules'), disabled: exportableRules.length === 0, onClick: exportRules },
                      ],
                    }}
                  >
                    <Button icon={<MoreOutlined />}>{t('more')}</Button>
                  </Dropdown>
                </Space>}

                {isMobile ? (
                  <RuleCardList
                    rows={mergedRows}
                    draggedIndex={draggedIndex}
                    dropTargetIndex={dropTargetIndex}
                    onHandlePointerDown={onHandlePointerDown}
                    openEdit={openEdit}
                    duplicateRule={duplicateRule}
                    moveUp={moveUp}
                    moveDown={moveDown}
                    confirmDelete={confirmDelete}
                    toggleRule={toggleRule}
                  />
                ) : (
                  <Table
                    columns={desktopColumns}
                    dataSource={mergedRows}
                    rowKey={(r) => r.key}
                    pagination={false}
                    scroll={{ x: tableScrollX }}
                    size="small"
                    className="routing-table"
                    onRow={(_record, index) => {
                      const classes: string[] = [];
                      const i = index ?? -1;
                      if (draggedIndex === i) classes.push('row-dragging');
                      if (dropTargetIndex === i && draggedIndex !== i && draggedIndex != null) {
                        classes.push(i > draggedIndex ? 'drop-after' : 'drop-before');
                      }
                      return { className: classes.join(' '), 'data-row-key': i } as React.HTMLAttributes<HTMLElement>;
                    }}
                  />
                )}
              </Space>
            ),
          },
          {
            key: 'tester',
            label: catTabLabel(<AimOutlined />, t('pages.xray.routeTester'), isMobile),
            children: <div className={standalone ? 'routing-settings-panel' : undefined}><RouteTester inboundTags={inboundTagOptions} isMobile={isMobile} disabled={rulesUnavailable || hasUnsavedChanges || routing.dirty} /></div>,
          },
        ]}
      />
      </div>
      {/* subjects is intent-only, exactly like exits: a template rule is
          written verbatim into the config, where every criterion works. */}
      <RuleFormModal
        open={ruleModalOpen}
        rule={editingRule}
        inboundTags={inboundTagOptions}
        outboundTags={outboundTagOptions}
        balancerTags={balancerTagOptions}
        onClose={() => setRuleModalOpen(false)}
        subjects={editingSource === 'intent' ? subjects : undefined}
        exits={editingSource === 'intent' ? exits : undefined}
        onConfirm={onRuleConfirm}
        disabled={rulesUnavailable}
      />
      <PromptModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        title={t('pages.xray.importRules')}
        okText={t('pages.xray.importRules')}
        type="textarea"
        json
        onConfirm={importRules}
      />
      <TextModal
        open={exportOpen}
        onClose={() => setExportOpen(false)}
        title={t('pages.xray.exportRules')}
        content={exportContent}
        fileName="routing-rules.json"
        json
      />
    </>
  );
}
