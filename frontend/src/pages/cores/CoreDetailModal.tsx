import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Empty, Modal, Space, Spin, Table, Tabs, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { CoreStatusView, InstanceStatusView } from '@/generated/zod';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import { CoreStateBadge } from '@/components/cores/CoreStateBadge';
import { Icon } from '@/components/ui/Icon';
import { Field, Kicker, PanelCard } from '@/components/ui/panel';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { coreColor, coreIcon, coreInstanceLabel, coreMaintenanceKey, coreRestarts, coreVersionLabel, isBuiltinCore, isKernelCore } from '@/lib/cores/display';
import { CoreLogsPanel } from './CoreLogsDrawer';
import { CoreVersionsPanel } from './CoreVersionsModal';
import { CoreStatus, CoreTrafficSlot, coreBytes, type CoreTab } from './CorePresentation';
import { coreUptime, type CoreMetrics } from './useCoreInventory';
import { TimeFormatter } from '@/utils';
import type { LiveTrafficSeries } from '@/hooks/useLiveTrafficHistory';

function CoreInbounds({ core }: { core: CoreStatusView }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const metadata = useCoresQuery();
  const inbounds = useInboundOptions();
  const descriptor = metadata.data?.find((entry) => entry.id === core.id);
  if (metadata.isPending || inbounds.isPending) return <Spin />;
  if (metadata.error || inbounds.error || !descriptor) return <Alert type="error" title={t('somethingWentWrong')}
    description={metadata.error?.message || inbounds.error?.message} action={<Button onClick={() => { void metadata.refetch(); void inbounds.refetch(); }}>{t('refresh')}</Button>} />;
  const rows = (inbounds.data || []).filter((inbound) => inbound.nodeId == null && descriptor.kinds.includes(inbound.protocol || ''));
  return <>
    {rows.length ? <div className="core-inbound-list">{rows.map((inbound) => <div key={inbound.id}>
      <strong>{inbound.remark || inbound.tag || `#${inbound.id}`}</strong><Tag>{inbound.protocol}</Tag><span className="p-sub">{inbound.port || '—'}</span>
    </div>)}</div> : <Empty description={t('pages.cores.noInbounds')} />}
    <Button onClick={() => navigate('/inbounds')}>{t('menu.inbounds')}</Button>
  </>;
}

export default function CoreDetailModal({ core, onClose, onRestart, restarting, onInstalled, initialTab = 'status', metrics, series }: {
  core: CoreStatusView; onClose: () => void; onRestart: () => void; restarting: boolean; onInstalled: () => void; initialTab?: CoreTab; metrics?: CoreMetrics; series?: LiveTrafficSeries;
}) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { isMobile } = useMediaQuery();
  const [tab, setTab] = useState<string>(initialTab);
  const when = (ms?: number) => ms ? new Date(ms).toLocaleString(i18n.language) : '—';
  const version = coreVersionLabel(core);
  const kernel = isKernelCore(core.id);
  const builtin = isBuiltinCore(core);
  const uptime = coreUptime(core);
  const columns: ColumnsType<InstanceStatusView> = [
    { title: t(coreInstanceLabel(core)), dataIndex: 'tag', render: (tag: string, row) => <span>{tag || `#${row.instanceId}`}{tag && <span className="core-id"> #{row.instanceId}</span>}</span> },
    { title: t('pages.cores.columns.state'), render: (_, row) => <CoreStateBadge state={row.state} ready={row.ready} detail={row.detail} /> },
    ...(!kernel ? [{ title: t(builtin ? 'pages.cores.panelProcessId' : 'pages.cores.processId'), dataIndex: 'pid', render: (pid?: number) => pid || '—' }] : []),
    ...(!kernel && !builtin ? [{ title: t('pages.cores.columns.restarts'), dataIndex: 'restarts', render: (n?: number) => n ?? '—' }] : []),
    { title: t('pages.cores.columns.since'), dataIndex: 'since', render: when },
    { title: t('pages.cores.columns.detail'), dataIndex: 'detail', render: (detail?: string) => detail || '—' },
  ];
  const status = <>
    <div className="p-card core-detail-band">
      <Field label={t('pages.cores.columns.state')} value={<CoreStatus core={core} />} />
      <Field label={t('pages.cores.fidelity.uptime')} value={uptime === undefined ? '—' : TimeFormatter.formatSecond(uptime)} />
      <Field label={t('pages.cores.fidelity.memory')} value="—" />
      <Field label={t('pages.cores.columns.restarts')} value={coreRestarts(core) ?? '—'} />
    </div>
    <PanelCard pad={10}><Kicker>{t('pages.cores.fidelity.trafficServed')}</Kicker><CoreTrafficSlot height={110} series={series} color={coreColor(core.id)} /></PanelCard>
    <div className="p-card core-detail-band">
      <Field label={t('menu.inbounds')} value={metrics?.inbounds?.length ?? '—'} />
      <Field label={t('menu.clients')} value={metrics?.clients ?? '—'} />
      <Field label={t('pages.cores.fidelity.traffic')} value={coreBytes(metrics?.traffic)} />
    </div>
    {[...new Set([core.unavailable, core.healthError, core.detail, core.trafficError].filter(Boolean))].map((detail) => <Alert key={detail} type={core.available ? 'warning' : 'info'} title={detail} showIcon />)}
    <Space wrap>
      <Button disabled={!core.available} loading={restarting} onClick={onRestart}>{t('pages.cores.restart')}</Button>
      <Button onClick={() => navigate(`/xray?core=${encodeURIComponent(core.id)}`)}>{t('menu.xray')}</Button>
    </Space>
  </>;
  const runtime = <>
    <p className="p-sub">{t(builtin ? 'pages.cores.builtInMaintenance' : kernel ? 'pages.cores.kernelRuntime' : 'pages.cores.processRuntime')}</p>
    <div className="core-detail-fields">
      {!kernel && <Field label={t(builtin ? 'pages.cores.panelProcessId' : 'pages.cores.processId')} value={core.pid || '—'} />}
      <Field label={t(coreInstanceLabel(core))} value={core.instances.length} />
      <Field label={t('pages.cores.columns.traffic')} value={core.trafficSeenAt ? when(core.trafficSeenAt) : t('pages.cores.trafficNever')} />
      <Field label={t('pages.cores.maintenance')} value={t(coreMaintenanceKey(core))} />
    </div>
    {core.instances.length ? <Table<InstanceStatusView> rowKey="instanceId" size="small" columns={columns} dataSource={core.instances} pagination={false} scroll={{ x: kernel ? 450 : 650 }} />
      : <Alert type="info" title={t('pages.cores.noInstanceRecords')} />}
    <Button onClick={() => navigate(`/xray?core=${encodeURIComponent(core.id)}`)}>{t('menu.xray')}</Button>
  </>;
  return <Modal open title={<Space wrap><span className="p-alert-i" style={{ color: coreColor(core.id), background: `color-mix(in srgb, ${coreColor(core.id)} 15%, transparent)` }}><Icon name={coreIcon(core.id)} /></span>{t(core.titleKey)}</Space>}
    className="core-detail-modal" width={820} footer={<Button onClick={onClose}>{t('close')}</Button>} onCancel={onClose}>
    <div className="core-detail-heading"><CoreStatus core={core} /><span className="p-mono">{version.text ?? t(version.key!)}</span></div>
    <Tabs activeKey={tab} onChange={setTab} tabPosition={isMobile ? 'top' : 'left'} destroyOnHidden items={[
      { key: 'status', label: t('pages.cores.columns.state'), children: status },
      { key: 'runtime', label: t('pages.cores.runtime'), children: runtime },
      { key: 'logging', label: t('pages.cores.logging'), children: core.logs ? <CoreLogsPanel core={core} /> : <Alert type="info" title={t('pages.cores.logsUnavailable')} /> },
      { key: 'inbounds', label: t('menu.inbounds'), children: <CoreInbounds core={core} /> },
      { key: 'version', label: t('pages.cores.columns.version'), children: <CoreVersionsPanel core={core} onInstalled={onInstalled} /> },
    ]} />
  </Modal>;
}
