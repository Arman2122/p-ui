import { useCallback, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Dropdown,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Radio,
  Space,
  Switch,
  Alert,
  Table,
  Tag,
  Tooltip,
  message,
} from 'antd';
import {
  PlusOutlined,
  CloudOutlined,
  ApiOutlined,
  MoreOutlined,
  RetweetOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  ArrowUpOutlined,
  ArrowDownOutlined,
  CheckCircleOutlined,
  WarningOutlined,
  ExportOutlined,
  ImportOutlined,
} from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import { useRoutingRulesQuery } from '@/api/queries/useRoutingQuery';
import { reservedOutboundTagMessage } from '@/schemas/xray';
import { onNumber } from '@/utils/onNumber';
import PromptModal from '@/components/feedback/PromptModal';
import TextModal from '@/components/feedback/TextModal';

import OutboundFormModal from './OutboundFormModal';
import { propagateOutboundTagRename } from '../basics/helpers';
import { planOutboundDeletion, applyOutboundDeletion } from '../reference-cleanup';
import DeletionImpactList from '../DeletionImpactList';
import { isBalancerLoopbackTag } from '../balancers/balancer-loopback';
import type { XraySettingsValue, SetTemplate, OutboundTestMode, OutboundTestState, OutboundTrafficRow } from '@/hooks/useXraySetting';
import './OutboundsTab.css';

import type { OutboundRow } from './outbounds-tab-types';
import { dialerProxyCycleTags, exitAsOutboundRow, isBuiltinOutbound, isUntestable, originalOutboundIndex } from './outbounds-tab-helpers';
import { useOutboundColumns, type OutboundColumnsParams } from './useOutboundColumns';
import OutboundCardList from './OutboundCardList';
import SubscriptionOutbounds from './SubscriptionOutbounds';
import ExitFormModal from './ExitFormModal';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import { useEgressesQuery } from '@/api/queries/useEgressesQuery';
import type { EgressPayload, EgressRecord } from '@/schemas/api/egress';
import HostEgressPreflight from './HostEgressPreflight';

interface OutboundSub {
  id: number;
  remark?: string;
  url?: string;
  enabled?: boolean;
  allowPrivate?: boolean;
  allowInsecure?: boolean;
  prepend?: boolean;
  priority?: number;
  tagPrefix?: string;
  updateInterval?: number;
  lastUpdated?: number;
  lastError?: string;
  outboundCount?: number;
}

interface OutboundsTabProps {
  standalone?: boolean;
  renderHeader?: (actions: ReactNode) => ReactNode;
  templateSettings: XraySettingsValue | null;
  setTemplateSettings: SetTemplate;
  outboundsTraffic: OutboundTrafficRow[];
  outboundTestStates: Record<number, OutboundTestState>;
  subscriptionTestStates: Record<string, OutboundTestState>;
  testingAll: boolean;
  inboundTags: string[];
  subscriptionOutbounds?: unknown[];
  subscriptionOutboundTags?: string[];
  isMobile: boolean;
  onResetTraffic: (tag: string) => void;
  onTest: (index: number, mode: string) => void;
  onTestSubscription: (outbound: Record<string, unknown>, mode: string) => void;
  onTestAll: (mode: string, exitKeys?: number[]) => void;
  onShowWarp: () => void;
  onShowNord: () => void;
  onRefreshXrayData?: () => void;
}

export default function OutboundsTab({
  standalone = false,
  renderHeader,
  templateSettings,
  setTemplateSettings,
  outboundsTraffic,
  outboundTestStates,
  subscriptionTestStates,
  testingAll,
  inboundTags: _inboundTags,
  subscriptionOutbounds,
  subscriptionOutboundTags,
  isMobile,
  onResetTraffic,
  onTest,
  onTestSubscription,
  onTestAll,
  onShowWarp,
  onShowNord,
  onRefreshXrayData,
}: OutboundsTabProps) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [testMode, setTestMode] = useState<OutboundTestMode>('tcp');
  const [modalOpen, setModalOpen] = useState(false);
  const [editingOutbound, setEditingOutbound] = useState<Record<string, unknown> | null>(null);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [existingTags, setExistingTags] = useState<string[]>([]);
  const egressMut = useEgressMutations();
  const egressesQuery = useEgressesQuery();
  const hostRulesQuery = useRoutingRulesQuery();
  const [busyExitId, setBusyExitId] = useState<number | null>(null);
  const [editingExit, setEditingExit] = useState<EgressRecord | null>(null);
  const [exitFormOpen, setExitFormOpen] = useState(false);

  // Subscription manager (CRUD + reorder + refresh + preview)
  const [subDrawerOpen, setSubDrawerOpen] = useState(false);
  const [subs, setSubs] = useState<OutboundSub[]>([]);
  const [subsLoading, setSubsLoading] = useState(false);
  const [newSub, setNewSub] = useState({ remark: '', url: '', tagPrefix: '', updateInterval: 600, enabled: true, allowPrivate: false, allowInsecure: false, prepend: false });
  const [editingSubId, setEditingSubId] = useState<number | null>(null);
  const [savingSub, setSavingSub] = useState(false);
  const [refreshingId, setRefreshingId] = useState<number | null>(null);
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [previewData, setPreviewData] = useState<{ tag?: string; protocol?: string }[] | null>(null);

  // Convenience: expose hours/minutes for the interval input
  const intervalHours = Math.floor((newSub.updateInterval || 600) / 3600);
  const intervalMinutes = Math.floor(((newSub.updateInterval || 600) % 3600) / 60);
  function setIntervalHM(h: number, m: number) {
    const secs = Math.max(60, (h || 0) * 3600 + (m || 0) * 60);
    setNewSub((prev) => ({ ...prev, updateInterval: secs }));
  }

  const outbounds = useMemo(
    () => (templateSettings?.outbounds || []) as unknown as OutboundRow[],
    [templateSettings?.outbounds],
  );

  const rows = useMemo(
    () =>
      outbounds
        .map((o, i) => ({ ...o, key: i }))
        .filter((o) => !isBalancerLoopbackTag(o.tag || '')),
    [outbounds],
  );
  const rowsRef = useRef<OutboundRow[]>([]);
  rowsRef.current = rows;

  /* Panel-owned rows are the fronts routing provisions and reaps for itself, so
     an operator neither made them nor can act on them. */
  const exitRows = useMemo(
    () => (egressesQuery.data ?? []).filter((row) => row.owner !== 'panel').map(exitAsOutboundRow),
    [egressesQuery.data],
  );
  /* Exits come last and the move/delete handlers still address `rows` by
     position, so an outbound's index means the same thing it always did. */
  const tableRows = useMemo(() => [...rows, ...exitRows], [rows, exitRows]);
  /* Test All has to be told which exits to probe: they are not in the xray
     config, so the hook that owns the results cannot find them by itself. */
  const testableExitKeys = useMemo(
    () => exitRows.filter((row) => !isUntestable(row)).map((row) => row.key),
    [exitRows],
  );

  const dialerProxyTags = useMemo(() => {
    const all = templateSettings?.outbounds || [];
    /* Not just the row being edited: an outbound that already dials through it,
       at any depth, closes the loop the other way round. F082. */
    const editingTag = editingIndex == null ? '' : (all[editingIndex]?.tag ?? '');
    const cycles = dialerProxyCycleTags(all, editingTag);
    const tags = new Set<string>();
    all.forEach((o, i) => {
      if (i === editingIndex) return;
      if (o?.protocol === 'blackhole') return;
      if (o?.tag && !cycles.has(o.tag)) tags.add(o.tag);
    });
    for (const tag of subscriptionOutboundTags || []) {
      if (tag) tags.add(tag);
    }
    return [...tags];
  }, [templateSettings?.outbounds, editingIndex, subscriptionOutboundTags]);

  const mutate = useCallback(
    (mutator: (next: XraySettingsValue) => void) => {
      setTemplateSettings((prev) => {
        if (!prev) return prev;
        const clone = JSON.parse(JSON.stringify(prev)) as XraySettingsValue;
        mutator(clone);
        return clone;
      });
    },
    [setTemplateSettings],
  );

  function openAdd() {
    setEditingOutbound(null);
    setEditingIndex(null);
    setExistingTags((templateSettings?.outbounds || []).map((o) => o?.tag).filter((tg): tg is string => !!tg));
    setModalOpen(true);
  }

  function openSubManager() {
    setSubDrawerOpen(true);
    loadSubs();
  }
  function openEdit(idx: number) {
    const target = originalOutboundIndex(rowsRef.current, idx);
    setEditingOutbound((templateSettings?.outbounds || [])[target] as Record<string, unknown>);
    setEditingIndex(target);
    setExistingTags(
      (templateSettings?.outbounds || [])
        .filter((_, i) => i !== target)
        .map((o) => o?.tag)
        .filter((tg): tg is string => !!tg),
    );
    setModalOpen(true);
  }
  function onConfirm(outbound: Record<string, unknown>) {
    if (!templateSettings) { messageApi.error(t('uiRepair.outbounds.templateUnavailable')); return; }
    const previous = editingIndex == null ? undefined : outbounds[editingIndex];
    if (previous && isBuiltinOutbound(previous) && (outbound.tag !== previous.tag || outbound.protocol !== previous.protocol)) {
      messageApi.error(t('uiRepair.outbounds.builtinFixed')); return;
    }
    mutate((tt) => {
      if (!Array.isArray(tt.outbounds)) tt.outbounds = [];
      const newTag = typeof outbound.tag === 'string' ? outbound.tag : '';
      if (editingIndex == null) {
        if (!newTag) return;
        tt.outbounds.push(outbound as never);
      } else {
        const oldTag = tt.outbounds[editingIndex]?.tag;
        tt.outbounds[editingIndex] = outbound as never;
        if (oldTag && newTag && oldTag !== newTag) {
          propagateOutboundTagRename(tt, oldTag, newTag);
        }
      }
    });
    setModalOpen(false);
  }

  /* An exit is a row the panel owns, not a line in the Xray config, so it is
     posted rather than folded into the template mutation above. */
  async function onConfirmExit(payload: EgressPayload) {
    try {
      const msg = await egressMut.add(payload);
      if (msg?.success) setModalOpen(false);
      else messageApi.error(msg?.msg || t('somethingWentWrong'));
    } catch { messageApi.error(t('somethingWentWrong')); }
  }

  function confirmDelete(idx: number) {
    const target = originalOutboundIndex(rowsRef.current, idx);
    if (outbounds[target] && isBuiltinOutbound(outbounds[target])) { messageApi.error(t('uiRepair.outbounds.builtinFixed')); return; }
    const impact = templateSettings
      ? planOutboundDeletion(templateSettings, target)
      : { rules: [], balancers: [], observatory: false, burst: false };
    modal.confirm({
      title: `${t('delete')} ${t('pages.xray.Outbounds')} #${idx + 1}?`,
      content: <DeletionImpactList impact={impact} />,
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: () => mutate((tt) => applyOutboundDeletion(tt, target)),
    });
  }
  function setFirst(idx: number) {
    const target = originalOutboundIndex(rowsRef.current, idx);
    mutate((tt) => {
      if (!tt.outbounds) return;
      const [moved] = tt.outbounds.splice(target, 1);
      tt.outbounds.unshift(moved);
    });
  }
  function moveUp(idx: number) {
    if (idx <= 0) return;
    const target = originalOutboundIndex(rowsRef.current, idx);
    const prev = originalOutboundIndex(rowsRef.current, idx - 1);
    mutate((tt) => {
      if (!tt.outbounds) return;
      [tt.outbounds[prev], tt.outbounds[target]] = [tt.outbounds[target], tt.outbounds[prev]];
    });
  }
  function moveDown(idx: number) {
    if (idx >= rowsRef.current.length - 1) return;
    const target = originalOutboundIndex(rowsRef.current, idx);
    const next = originalOutboundIndex(rowsRef.current, idx + 1);
    mutate((tt) => {
      if (!tt.outbounds) return;
      [tt.outbounds[next], tt.outbounds[target]] = [tt.outbounds[target], tt.outbounds[next]];
    });
  }

  const [importOpen, setImportOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [exportContent, setExportContent] = useState('');

  function exportOutbounds() {
    setExportContent(JSON.stringify(outbounds, null, 2));
    setExportOpen(true);
  }

  function importOutbounds(value: string) {
    if (!templateSettings) { messageApi.error(t('uiRepair.outbounds.templateUnavailable')); return; }
    let parsed: unknown;
    try {
      parsed = JSON.parse(value);
    } catch {
      messageApi.error(t('pages.xray.importInvalidJson'));
      return;
    }
    const obj = parsed as { outbounds?: unknown };
    const list = Array.isArray(parsed) ? parsed : Array.isArray(obj?.outbounds) ? obj.outbounds : null;
    if (!list) {
      messageApi.error(t('pages.xray.importInvalidJson'));
      return;
    }
    const seen = new Set(outbounds.map((outbound) => outbound.tag));
    for (const row of list) {
      if (!row || typeof row !== 'object' || Array.isArray(row) || typeof row.tag !== 'string' || !row.tag.trim()
        || typeof row.protocol !== 'string' || !row.protocol.trim() || reservedOutboundTagMessage(row.tag) || seen.has(row.tag)) {
        messageApi.error(t('uiRepair.outbounds.importInvalidOutbound')); return;
      }
      seen.add(row.tag);
    }
    mutate((tt) => {
      if (!Array.isArray(tt.outbounds)) tt.outbounds = [];
      tt.outbounds.push(...(list as never[]));
    });
    setImportOpen(false);
  }

  // --- Subscription management (minimal inline UI) ---
  async function loadSubs() {
    setSubsLoading(true);
    try {
      const r = await HttpUtil.get('/panel/api/xray/outbound-subs');
      if (r?.success) setSubs(Array.isArray(r.obj) ? r.obj : []);
      else messageApi.error(r?.msg || t('pages.xray.outboundSub.toastLoadFailed'));
    } catch {
      messageApi.error(t('pages.xray.outboundSub.toastLoadFailed'));
    } finally {
      setSubsLoading(false);
    }
  }
  function subBody(src: { remark?: string; url?: string; tagPrefix?: string; updateInterval?: number; enabled?: boolean; allowPrivate?: boolean; allowInsecure?: boolean; prepend?: boolean }) {
    return {
      remark: src.remark ?? '',
      url: src.url ?? '',
      tagPrefix: src.tagPrefix ?? '',
      updateInterval: src.updateInterval ?? 600,
      enabled: src.enabled ?? true,
      allowPrivate: src.allowPrivate ?? false,
      allowInsecure: src.allowInsecure ?? false,
      prepend: src.prepend ?? false,
    };
  }
  function resetSubForm() {
    setNewSub({ remark: '', url: '', tagPrefix: '', updateInterval: 600, enabled: true, allowPrivate: false, allowInsecure: false, prepend: false });
    setEditingSubId(null);
    setPreviewData(null);
  }
  function openEditSub(sub: OutboundSub) {
    setNewSub({
      remark: sub.remark ?? '',
      url: sub.url ?? '',
      tagPrefix: sub.tagPrefix ?? '',
      updateInterval: sub.updateInterval ?? 600,
      enabled: sub.enabled ?? true,
      allowPrivate: sub.allowPrivate ?? false,
      allowInsecure: sub.allowInsecure ?? false,
      prepend: sub.prepend ?? false,
    });
    setEditingSubId(sub.id);
    setPreviewData(null);
  }
  async function saveSub() {
    if (!newSub.url.trim()) {
      messageApi.warning(t('pages.xray.outboundSub.toastUrlRequired'));
      return;
    }
    setSavingSub(true);
    try {
      const url = editingSubId != null
        ? `/panel/api/xray/outbound-subs/${editingSubId}`
        : '/panel/api/xray/outbound-subs';
      const r = await HttpUtil.post<OutboundSub>(url, subBody(newSub));
      if (r?.success) {
        messageApi.success(t(editingSubId != null ? 'pages.xray.outboundSub.toastUpdated' : 'pages.xray.outboundSub.toastAdded'));
        const createdId = editingSubId == null ? r.obj?.id : undefined;
        resetSubForm();
        await loadSubs();
        if (createdId) await refreshOne(createdId);
        onRefreshXrayData?.();
      } else {
        messageApi.error(r?.msg || t('pages.xray.outboundSub.toastAddFailed'));
      }
    } catch {
      messageApi.error(t('pages.xray.outboundSub.toastAddFailed'));
    } finally {
      setSavingSub(false);
    }
  }
  async function previewSub() {
    if (!newSub.url.trim()) {
      messageApi.warning(t('pages.xray.outboundSub.toastUrlRequired'));
      return;
    }
    setPreviewing(true);
    setPreviewData(null);
    try {
      const r = await HttpUtil.post<{ tag?: string; protocol?: string }[]>('/panel/api/xray/outbound-subs/parse', { url: newSub.url, allowPrivate: newSub.allowPrivate, allowInsecure: newSub.allowInsecure });
      if (r?.success && Array.isArray(r.obj)) {
        setPreviewData(r.obj);
        if (r.obj.length === 0) messageApi.info(t('pages.xray.outboundSub.previewEmpty'));
      } else {
        messageApi.error(r?.msg || t('pages.xray.outboundSub.previewEmpty'));
      }
    } catch {
      messageApi.error(t('pages.xray.outboundSub.previewEmpty'));
    } finally {
      setPreviewing(false);
    }
  }
  async function toggleEnabled(sub: OutboundSub) {
    setBusyId(sub.id);
    try {
      const r = await HttpUtil.post(`/panel/api/xray/outbound-subs/${sub.id}`, subBody({ ...sub, enabled: !sub.enabled }));
      if (r?.success) {
        await loadSubs();
        onRefreshXrayData?.();
      } else {
        messageApi.error(r?.msg || t('pages.xray.outboundSub.toastAddFailed'));
      }
    } catch {
      messageApi.error(t('pages.xray.outboundSub.toastAddFailed'));
    } finally {
      setBusyId(null);
    }
  }
  async function moveSub(id: number, dir: 'up' | 'down') {
    setBusyId(id);
    try {
      const r = await HttpUtil.post(`/panel/api/xray/outbound-subs/${id}/move`, { dir });
      if (r?.success) {
        await loadSubs();
        onRefreshXrayData?.();
      } else {
        messageApi.error(r?.msg || t('uiRepair.outbounds.subscriptionMoveFailed'));
      }
    } catch {
      messageApi.error(t('uiRepair.outbounds.subscriptionMoveFailed'));
    } finally {
      setBusyId(null);
    }
  }
  async function refreshOne(id: number) {
    setRefreshingId(id);
    try {
      const r = await HttpUtil.post(`/panel/api/xray/outbound-subs/${id}/refresh`);
      if (r?.success) {
        messageApi.success(t('pages.xray.outboundSub.toastRefreshed'));
        await loadSubs();
        onRefreshXrayData?.();
      } else {
        messageApi.error(r?.msg || t('pages.xray.outboundSub.toastRefreshFailed'));
      }
    } catch {
      messageApi.error(t('pages.xray.outboundSub.toastRefreshFailed'));
    } finally {
      setRefreshingId(null);
    }
  }
  async function refreshAllSubs() {
    if (subs.length === 0) return;
    setRefreshingAll(true);
    try {
      let failed = 0;
      for (const s of subs) {
        try { if (!(await HttpUtil.post(`/panel/api/xray/outbound-subs/${s.id}/refresh`))?.success) failed += 1; } catch { failed += 1; }
      }
      if (failed) messageApi.error(t('uiRepair.outbounds.subscriptionRefreshPartial', { failed, total: subs.length }));
      else messageApi.success(t('pages.xray.outboundSub.toastRefreshed'));
      await loadSubs();
      onRefreshXrayData?.();
    } finally {
      setRefreshingAll(false);
    }
  }
  async function deleteOne(id: number) {
    try {
      const r = await HttpUtil.post(`/panel/api/xray/outbound-subs/${id}/del`);
      if (r?.success) {
        messageApi.success(t('pages.xray.outboundSub.toastDeleted'));
        await loadSubs();
        onRefreshXrayData?.();
      }
    } catch {
      messageApi.error(t('pages.xray.outboundSub.toastDeleteFailed'));
    }
  }

  function editExit(exit: EgressRecord) {
    setEditingExit(exit);
    setExitFormOpen(true);
  }

  async function toggleExit(exit: EgressRecord) {
    setBusyExitId(exit.id);
    try { const result = await egressMut.update(exit.id, {
      id: exit.id,
      type: exit.type,
      enable: !exit.enable,
      remark: exit.remark,
      target: exit.target,
      settings: exit.settings,
      owner: exit.owner,
      ingressInboundId: exit.ingressInboundId,
    }); if (!result?.success) messageApi.error(result?.msg || t('somethingWentWrong')); } catch { messageApi.error(t('somethingWentWrong')); }
    finally { setBusyExitId(null); }
  }

  function deleteExit(exit: EgressRecord) {
    modal.confirm({
      title: `${t('delete')} ${exit.remark || `#${exit.id}`}?`,
      content: t('pages.xray.egress.deleteConfirm'),
      okText: t('confirm'),
      cancelText: t('cancel'),
      onOk: async () => {
        const result = await egressMut.remove(exit.id);
        if (!result?.success) throw new Error(result?.msg || t('somethingWentWrong'));
      },
    });
  }

  const controls: OutboundColumnsParams = {
    testMode,
    rows,
    outboundsTraffic,
    outboundTestStates,
    openEdit,
    setFirst,
    moveUp,
    moveDown,
    confirmDelete,
    onResetTraffic,
    onTest,
    editExit,
    toggleExit,
    deleteExit,
    templateSettings,
    hostRules: hostRulesQuery.data,
    busyExitId,
  };
  const columns = useOutboundColumns(controls);
  const canTestAll = tableRows.some((row) => !isUntestable(row))
    || (subscriptionOutbounds ?? []).some((outbound) => !!(outbound as OutboundRow)?.tag && !isUntestable(outbound as OutboundRow));
  const primaryActions = <>
    <Button loading={testingAll} disabled={!canTestAll} icon={<PlayCircleOutlined />} onClick={() => onTestAll(testMode, testableExitKeys)}>{t('pages.xray.outbound.testAll')}</Button>
    <Dropdown trigger={['click']} menu={{ items: [
      { key: 'subscriptions', icon: <CloudOutlined />, label: t('pages.xray.outboundSub.manage'), onClick: openSubManager },
      { key: 'warp', icon: <CloudOutlined />, label: 'WARP', onClick: onShowWarp },
      { key: 'nord', icon: <ApiOutlined />, label: 'NordVPN', onClick: onShowNord },
      { type: 'divider' },
      { key: 'import', icon: <ImportOutlined />, label: t('pages.xray.importOutbounds'), disabled: !templateSettings, onClick: () => setImportOpen(true) },
      { key: 'export', icon: <ExportOutlined />, label: t('pages.xray.exportOutbounds'), disabled: !outbounds.length, onClick: exportOutbounds },
    ] }}><Button icon={<MoreOutlined />}>{t('more')}</Button></Dropdown>
    <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>{t('uiRepair.outbounds.addOutbound')}</Button>
  </>;

  return (
    <div className="outbounds-tab">
      {modalContextHolder}
      {messageContextHolder}
      {standalone ? (renderHeader ? renderHeader(primaryActions) : <PageHeader title={t('menu.outbounds')} sub={t('pages.xray.sectionDesc_outbound')} actions={primaryActions} />)
        : <div className="outbounds-embedded-actions">{primaryActions}</div>}
      <PanelCard pad={0} className="outbounds-card">
        <div className="outbounds-toolbar">
          <Tooltip title={t('pages.xray.outbound.testModeTooltip')}>
            <Radio.Group value={testMode} onChange={(event) => setTestMode(event.target.value)} buttonStyle="solid" size="small">
              <Radio.Button value="tcp">TCP</Radio.Button>
              <Radio.Button value="http">HTTP</Radio.Button>
              <Radio.Button value="real">{t('pages.xray.outbound.modeRealDelay')}</Radio.Button>
            </Radio.Group>
          </Tooltip>
          <span className="p-sub">{t('uiRepair.outbounds.saveHint')}</span>
          <Popconfirm placement="topRight" okText={t('reset')} cancelText={t('cancel')} title={t('pages.inbounds.resetAllTrafficContent')}
            onConfirm={() => onResetTraffic('-alltags-')}>
            <Button size="small" aria-label={t('pages.inbounds.resetTraffic')} icon={<RetweetOutlined />} />
          </Popconfirm>
        </div>
        {(egressesQuery.isError || hostRulesQuery.isError) && <Alert type="warning" showIcon
          title={t('uiRepair.outbounds.metadataUnavailable')} action={<Button size="small" onClick={() => { void egressesQuery.refetch(); void hostRulesQuery.refetch(); }}>{t('refresh')}</Button>} />}
        {!!egressesQuery.data?.length && <HostEgressPreflight />}

        {isMobile ? (
          <OutboundCardList
            rows={tableRows}
            controls={controls}
          />
        ) : (
          <Table
            className="p-table outbounds-table"
            columns={columns}
            dataSource={tableRows}
            rowKey={(r) => r.key}
            pagination={false}
            size="small"
            tableLayout="fixed"
            scroll={{ x: 985 }}
            locale={{
              emptyText: (
                <div className="card-empty">
                  <ExportOutlined style={{ fontSize: 32, marginBottom: 8 }} />
                  <div>{t('noData')}</div>
                </div>
              ),
            }}
          />
        )}

      </PanelCard>
        <OutboundFormModal
          open={modalOpen}
          outbound={editingOutbound}
          existingTags={existingTags}
          dialerProxyTags={dialerProxyTags}
          onClose={() => setModalOpen(false)}
          onConfirm={onConfirm}
          onConfirmExit={onConfirmExit}
        />
        <PromptModal
          open={importOpen}
          onClose={() => setImportOpen(false)}
          title={t('pages.xray.importOutbounds')}
          okText={t('pages.xray.importOutbounds')}
          type="textarea"
          json
          onConfirm={importOutbounds}
        />
        <TextModal
          open={exportOpen}
          onClose={() => setExportOpen(false)}
          title={t('pages.xray.exportOutbounds')}
          content={exportContent}
          fileName="outbounds.json"
          json
        />

        {/* Subscription outbounds (read-only, merged at runtime) */}
        {Array.isArray(subscriptionOutbounds) && subscriptionOutbounds.length > 0 && (
          <SubscriptionOutbounds
            templateSettings={templateSettings}
            hostRules={hostRulesQuery.data}
            subscriptionOutbounds={subscriptionOutbounds}
            outboundsTraffic={outboundsTraffic}
            subscriptionTestStates={subscriptionTestStates}
            testMode={testMode}
            isMobile={isMobile}
            onTestSubscription={onTestSubscription}
          />
        )}

        <ExitFormModal open={exitFormOpen} exit={editingExit} onClose={() => setExitFormOpen(false)} />
      <Modal
        title={t('pages.xray.outboundSub.title')}
        open={subDrawerOpen}
        onCancel={() => setSubDrawerOpen(false)}
        footer={null}
        width={isMobile ? '100%' : 640}
        destroyOnHidden
      >
        <Space orientation="vertical" style={{ width: '100%' }} size="large">
          <div>
            {editingSubId != null && (
              <div style={{ marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
                <Tag color="blue">{t('edit')}</Tag>
                <span style={{ fontWeight: 600 }}>{newSub.remark || newSub.url}</span>
              </div>
            )}
            <Form layout="vertical" size="small">
              <Form.Item label={t('pages.xray.outboundSub.remark')}>
                <Input value={newSub.remark} onChange={(e) => setNewSub({ ...newSub, remark: e.target.value })} placeholder={t('pages.xray.outboundSub.remarkPlaceholder')} />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.url')} required>
                <Input value={newSub.url} onChange={(e) => setNewSub({ ...newSub, url: e.target.value })} placeholder={t('pages.xray.outboundSub.urlPlaceholder')} />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.tagPrefix')}>
                <Input value={newSub.tagPrefix} onChange={(e) => setNewSub({ ...newSub, tagPrefix: e.target.value })} placeholder={t('pages.xray.outboundSub.tagPrefixPlaceholder')} />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.interval')}>
                <Space>
                  <InputNumber
                    min={0}
                    value={intervalHours}
                    onChange={onNumber((v) => setIntervalHM(v, intervalMinutes))}
                    style={{ width: 80 }}
                  /> {t('pages.xray.outboundSub.hours')}
                  <InputNumber
                    min={0}
                    max={59}
                    value={intervalMinutes}
                    onChange={onNumber((v) => setIntervalHM(intervalHours, v))}
                    style={{ width: 80 }}
                  /> {t('pages.xray.outboundSub.minutes')}
                </Space>
                <div style={{ fontSize: 12, color: '#888', marginTop: 4 }}>
                  {t('pages.xray.outboundSub.intervalHint')}
                </div>
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.enabled')}>
                <Switch checked={newSub.enabled} onChange={(v) => setNewSub({ ...newSub, enabled: v })} />
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.allowPrivate')}>
                <Switch checked={newSub.allowPrivate} onChange={(v) => setNewSub({ ...newSub, allowPrivate: v })} />
                <div style={{ fontSize: 12, color: '#888', marginTop: 4 }}>
                  {t('pages.xray.outboundSub.allowPrivateHint')}
                </div>
              </Form.Item>
              <Form.Item label={t('pages.hosts.fields.allowInsecure')}>
                <Switch checked={newSub.allowInsecure} onChange={(v) => setNewSub({ ...newSub, allowInsecure: v })} />
                <div style={{ fontSize: 12, color: '#888', marginTop: 4 }}>
                  {t('pages.hosts.hints.allowInsecure')}
                </div>
              </Form.Item>
              <Form.Item label={t('pages.xray.outboundSub.prepend')}>
                <Switch checked={newSub.prepend} onChange={(v) => setNewSub({ ...newSub, prepend: v })} />
                <div style={{ fontSize: 12, color: '#888', marginTop: 4 }}>
                  {t('pages.xray.outboundSub.prependHint')}
                </div>
              </Form.Item>
              <Space wrap>
                <Button type="primary" onClick={saveSub} loading={savingSub} icon={editingSubId != null ? <EditOutlined /> : <PlusOutlined />}>
                  {editingSubId != null ? t('save') : t('pages.xray.outboundSub.addButton')}
                </Button>
                <Button onClick={previewSub} loading={previewing} icon={<EyeOutlined />}>
                  {t('pages.xray.outboundSub.preview')}
                </Button>
                {editingSubId != null && <Button onClick={resetSubForm}>{t('cancel')}</Button>}
              </Space>
              {previewData && previewData.length > 0 && (
                <div style={{ marginTop: 8 }}>
                  <div style={{ fontSize: 12, color: '#888', marginBottom: 4 }}>{previewData.length} · {t('pages.xray.Outbounds')}</div>
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, maxHeight: 120, overflow: 'auto' }}>
                    {previewData.map((o, i) => (
                      <Tag key={i}>{o?.tag || '—'}{o?.protocol ? ` · ${o.protocol}` : ''}</Tag>
                    ))}
                  </div>
                </div>
              )}
            </Form>
          </div>

          <div>
            <div style={{ fontWeight: 600, marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
              {t('pages.xray.outboundSub.active')}
              <Button aria-label={t('refresh')} size="small" icon={<ReloadOutlined />} onClick={loadSubs} loading={subsLoading} />
              {subs.length > 0 && (
                <Button size="small" type="primary" icon={<ReloadOutlined />} onClick={refreshAllSubs} loading={refreshingAll}>
                  {t('pages.xray.outboundSub.refreshAll')}
                </Button>
              )}
            </div>
            {subs.length === 0 ? (
              <div style={{ color: '#888' }}>{t('pages.xray.outboundSub.empty')}</div>
            ) : (
              <Table
                size="small"
                dataSource={subs}
                rowKey={(r) => r.id}
                pagination={false}
                scroll={{ x: true }}
                columns={[
                  {
                    title: '',
                    key: 'order',
                    width: 56,
                    render: (_: unknown, r: OutboundSub, index: number) => (
                      <Space size={0}>
                        <Button aria-label={t('pages.inbounds.form.moveUp')} type="text" size="small" icon={<ArrowUpOutlined />} disabled={index === 0 || busyId === r.id} onClick={() => moveSub(r.id, 'up')} />
                        <Button aria-label={t('pages.inbounds.form.moveDown')} type="text" size="small" icon={<ArrowDownOutlined />} disabled={index === subs.length - 1 || busyId === r.id} onClick={() => moveSub(r.id, 'down')} />
                      </Space>
                    ),
                  },
                  {
                    title: t('pages.xray.outboundSub.colRemark'),
                    key: 'remark',
                    render: (_: unknown, r: OutboundSub) => (
                      <div>
                        <div>{r.remark || <em>{t('pages.xray.outboundSub.auto')}</em>}</div>
                        {r.tagPrefix && <div style={{ fontSize: 11, color: '#888' }}>{r.tagPrefix}</div>}
                      </div>
                    ),
                  },
                  { title: t('pages.xray.Outbounds'), dataIndex: 'outboundCount', key: 'outboundCount', align: 'center', render: (v) => v ?? 0 },
                  {
                    title: t('status'),
                    key: 'status',
                    align: 'center',
                    render: (_: unknown, r: OutboundSub) => (r.lastError
                      ? <Tooltip title={r.lastError}><WarningOutlined style={{ color: '#e04141' }} /></Tooltip>
                      : <Tooltip title={t('pages.xray.outboundSub.statusOk')}><CheckCircleOutlined style={{ color: '#008771' }} /></Tooltip>),
                  },
                  { title: t('pages.xray.outboundSub.colLastFetch'), dataIndex: 'lastUpdated', key: 'lastUpdated', render: (v: number) => v ? new Date(v * 1000).toLocaleString() : t('pages.xray.outboundSub.never') },
                  {
                    title: t('pages.xray.outboundSub.colEnabled'),
                    key: 'enabled',
                    align: 'center',
                    render: (_: unknown, r: OutboundSub) => <Switch size="small" checked={!!r.enabled} loading={busyId === r.id} onChange={() => toggleEnabled(r)} />,
                  },
                  {
                    title: '',
                    key: 'actions',
                    render: (_: unknown, r: OutboundSub) => (
                      <Space>
                        <Button aria-label={t('edit')} size="small" icon={<EditOutlined />} onClick={() => openEditSub(r)} title={t('edit')} />
                        <Button aria-label={t('pages.xray.outboundSub.refreshNow')} size="small" icon={<ReloadOutlined />} loading={refreshingId === r.id} onClick={() => refreshOne(r.id)} title={t('pages.xray.outboundSub.refreshNow')} />
                        <Popconfirm title={t('pages.xray.outboundSub.deleteConfirm')} okText={t('delete')} cancelText={t('cancel')} onConfirm={() => deleteOne(r.id)}>
                          <Button aria-label={t('delete')} size="small" danger icon={<DeleteOutlined />} />
                        </Popconfirm>
                      </Space>
                    ),
                  },
                ]}
              />
            )}
            <div style={{ marginTop: 8, fontSize: 12, color: '#666' }}>
              {t('pages.xray.outboundSub.restartHint')}
            </div>
          </div>
        </Space>
      </Modal>
    </div>
  );
}
