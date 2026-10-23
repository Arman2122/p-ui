import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Checkbox, Dropdown, Input, Modal, Segmented, Space, Tag, Tooltip } from 'antd';
import { useNavigate } from 'react-router';
import { PageHeader, PanelCard, Gauge, Field, Kicker, StatusChip, Toolbar } from '@/components/ui/panel';
import { StatStrip } from '@/components/ui';
import { Icon } from '@/components/ui/Icon';
import { TimeFormatter, SizeFormatter } from '@/utils';
import { isPanelUpdateAvailable } from '@/lib/panel-version';
import type { NodeRecord } from '@/schemas/node';
import { nodeHasReadings, nodeTraffic } from './telemetry';
import NodeList, { type NodeListProps } from './NodeList';
import NodeHistoryPanel from './NodeHistoryPanel';
import NodeThroughput from './NodeThroughput';
import './NodeFleet.css';

function NodeResources({ node }: { node: NodeRecord }) {
  const { t } = useTranslation();
  return <div className="p-noderings">
    {[{ key: 'cpu', label: t('pages.nodes.cpu'), value: node.cpuPct, color: '#4c8dff' },
      { key: 'memory', label: t('pages.nodes.mem'), value: node.memPct, color: '#5ac8b0' },
      { key: 'disk', label: t('nodesFidelity.disk'), value: undefined, color: '#c79a4b' },
    ].map(resource => {
      const reported = nodeHasReadings(node) && resource.value != null;
      return <div className="node-resource" key={resource.key} aria-label={`${resource.label}: ${reported ? `${resource.value}%` : t('nodesFidelity.notReported')}`}>
        <span className="node-resource-ring"><Gauge value={resource.value ?? 0} muted={!reported} tone={resource.color} size={52} thick={4} /></span>
        <div className="node-resource-copy">
          <span className="node-resource-value">{reported ? `${resource.value!.toFixed(resource.key === 'cpu' ? 1 : 0)}%` : '—'}</span>
          <Kicker>{resource.label}</Kicker>
          {!reported && <span className="node-resource-note">{t('nodesFidelity.notReported')}</span>}
        </div>
      </div>;
    })}
  </div>;
}

export default function NodeFleet(props: NodeListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [view, setView] = useState('cards');
  const [search, setSearch] = useState('');
  const [detailKey, setDetailKey] = useState<string | null>(null);
  const keyOf = (node: NodeRecord) => node.transitive ? `t-${node.guid}` : String(node.id);
  const detail = props.nodes.find(node => keyOf(node) === detailKey);
  const nodes = useMemo(() => props.nodes.filter(node => `${node.name ?? ''} ${node.address ?? ''} ${node.remark ?? ''}`.toLowerCase().includes(search.toLowerCase())), [props.nodes, search]);
  const showDetails = useCallback((node: NodeRecord) => setDetailKey(node.transitive ? `t-${node.guid}` : String(node.id)), []);
  const health = (node: NodeRecord) => {
    const state = node.xrayState?.trim().toLowerCase();
    const failed = node.status === 'online' && ['stop', 'error', 'stopped', 'failed'].includes(state ?? '');
    const key = node.enable === false ? 'disabled' : failed ? 'failed' : node.status === 'online' ? 'online' : node.status === 'offline' ? 'offline' : 'unknown';
    return { key, label: key === 'disabled' ? t('disabled') : key === 'failed' ? t('pages.cores.state.failed') : t(`pages.nodes.statusValues.${key}`),
      color: key === 'online' ? 'var(--p-success)' : key === 'failed' ? 'var(--p-warning)' : key === 'offline' ? 'var(--p-error)' : 'var(--p-text-2)' };
  };
  const direct = props.nodes.filter(node => !node.transitive);
  const healthy = props.nodes.filter(node => health(node).key === 'online').length;
  const updates = direct.filter(node => isPanelUpdateAvailable(props.latestVersion ?? '', node.panelVersion ?? '')).length;
  const sumReported = (values: (number | undefined)[]) => values.length === 0 ? 0 : values.every(value => value != null) ? values.reduce<number>((sum, value) => sum + value!, 0) : undefined;
  const traffic = sumReported(direct.map(nodeTraffic));
  const latencies = direct.filter(node => nodeHasReadings(node) && node.latencyMs != null).map(node => node.latencyMs!);
  const latency = (node: NodeRecord) => {
    const tone = !nodeHasReadings(node) || node.latencyMs == null ? 'var(--p-text-2)' : node.latencyMs < 20 ? 'var(--p-success)' : node.latencyMs < 60 ? 'var(--p-warning)' : 'var(--p-error)';
    return <span className="p-chip node-latency" style={{ color: `color-mix(in srgb, ${tone} 58%, var(--p-ink))`, borderColor: `color-mix(in srgb, ${tone} 38%, transparent)`, background: `color-mix(in srgb, ${tone} 10%, transparent)` }}>
      <Icon name="bolt" size={11} />{nodeHasReadings(node) && node.latencyMs != null ? `${node.latencyMs} ms` : '—'}
    </span>;
  };
  const heartbeat = (node: NodeRecord) => node.lastHeartbeat ? TimeFormatter.formatSecond(Math.max(0, Math.floor(Date.now() / 1000) - node.lastHeartbeat)) : t('pages.nodes.never');
  const address = (node: NodeRecord) => node.address ? `${node.address.includes(':') && !node.address.startsWith('[') ? `[${node.address}]` : node.address}${node.port ? `:${node.port}` : ''}` : '—';
  const parentName = (node: NodeRecord) => props.nodes.find(parent => parent.guid === node.parentGuid)?.name || node.parentGuid || '—';
  const actions = (node: NodeRecord) => node.transitive ? <Tooltip title={t('pages.nodes.subNodeTip', { parent: parentName(node) })}><Tag>{t('pages.nodes.subNode')}</Tag></Tooltip> : <>
    <Button size="small" icon={<Icon name="bolt" size={14} />} onClick={() => props.onProbe(node)}>{t('check')}</Button>
    <Button size="small" icon={<Icon name="chart" size={14} />} onClick={() => setDetailKey(keyOf(node))}>{t('designParityNodes.historyAction', { defaultValue: 'History' })}</Button>
    <Dropdown trigger={['click']} menu={{ items: [
      { key: 'edit', label: t('edit'), onClick: () => props.onEdit(node) },
      { key: 'update', label: t('pages.nodes.updatePanel'), disabled: !node.enable || node.status !== 'online', onClick: () => props.onUpdateNode(node) },
      { key: 'enable', label: t(node.enable ? 'pages.clients.disable' : 'pages.clients.enable'), onClick: () => props.onToggleEnable(node, !node.enable) },
      { type: 'divider' },
      { key: 'delete', label: t('delete'), danger: true, onClick: () => props.onDelete(node) },
    ] }}><Button size="small" type="text" aria-label={t('more')} icon={<Icon name="more" size={15} />} /></Dropdown>
  </>;
  const inbounds = (node: NodeRecord) => <div className="node-inbounds">
    <div className="node-inbounds-heading"><Kicker>{t('designParityNodes.inboundsServed', { defaultValue: 'Inbounds served' })}</Kicker><span className="p-mono">{address(node)}</span></div>
    <Space size={4} wrap>{node.servedInbounds == null ? <span className="p-sub">{t('nodesFidelity.notReported')}</span> : node.servedInbounds.length === 0 ? <span className="p-sub">{t('noData')}</span> : node.servedInbounds.map(inbound => <Tag color="blue" key={inbound.id}>{inbound.tag} · {inbound.protocol}</Tag>)}</Space>
  </div>;
  return <div className="p-network-page node-fleet">
    <PageHeader title={t('menu.nodes')} sub={t('pages.nodes.subtitle')} actions={<>
      <Segmented size="small" aria-label={t('nodesFidelity.view')} value={view} onChange={setView} options={[{ value: 'cards', label: t('design.cardView') }, { value: 'table', label: t('design.tableView') }]} />
      {props.onRefresh && <Button icon={<Icon name="refresh" size={15} />} loading={props.loading} onClick={props.onRefresh}>{t('refresh')}</Button>}
      <Button icon={<Icon name="shield" size={15} />} onClick={props.onMtls}>{t('pages.nodes.mtls.title')}</Button>
      <Button type="primary" icon={<Icon name="plus" size={15} />} onClick={props.onAdd}>{t('pages.nodes.addNode')}</Button>
    </>} />
    <StatStrip items={[
      { key: 'nodes', label: t('pages.nodes.totalNodes'), value: props.nodes.length },
      { key: 'healthy', label: t('designParityNodes.healthy', { defaultValue: 'Healthy' }), value: `${healthy} / ${props.nodes.length}`, color: healthy === props.nodes.length ? 'var(--p-success)' : 'var(--p-warning)', detail: <Space direction="vertical"><span>{t('pages.nodes.onlineNodes')}: {props.nodes.filter(node => node.enable && node.status === 'online').length}</span><span>{t('pages.nodes.offlineNodes')}: {props.nodes.filter(node => node.enable && node.status === 'offline').length}</span></Space> },
      { key: 'updates', label: t('designParityNodes.panelUpdates', { defaultValue: 'Panel updates' }), value: props.latestVersion ? updates : '—', color: updates ? 'var(--p-warning)' : undefined },
      { key: 'clients', label: t('clients'), value: sumReported(direct.map(node => node.clientCount)) ?? '—' },
      { key: 'traffic', label: t('telemetry.inboundTraffic'), value: traffic == null ? '—' : SizeFormatter.sizeFormat(traffic) },
      { key: 'latency', label: t('pages.nodes.avgLatency'), value: latencies.length ? `${Math.round(latencies.reduce((sum, value) => sum + value, 0) / latencies.length)} ms` : '—' },
    ]} />
    <section className="p-card node-fleet-list">
      <Toolbar left={<Input type="search" prefix={<Icon name="search" size={15} />} value={search} onChange={event => setSearch(event.target.value)} allowClear aria-label={t('design.searchNodes')} placeholder={t('design.searchNodes')} />}
        right={<><span className="p-sub">{nodes.length} / {props.nodes.length}</span>{!!props.selectedIds.length && <Button size="small" onClick={props.onUpdateSelected}>{t('pages.nodes.updateSelected', { count: props.selectedIds.length })}</Button>}</>} />
      {view === 'table' && <NodeList {...props} nodes={nodes} isMobile={false} hideToolbar onDetails={showDetails} />}
    </section>
    {view === 'cards' && <div className="p-groupgrid">
      {nodes.map(node => <PanelCard key={keyOf(node)} className="node-fleet-card" pad={0} title={<div className="node-card-heading">
        {!node.transitive && <Checkbox checked={props.selectedIds.includes(node.id)} disabled={!node.enable || node.status !== 'online'} aria-label={node.name || String(node.id)} onChange={event => props.onSelectionChange(event.target.checked ? [...props.selectedIds, node.id] : props.selectedIds.filter(id => id !== node.id))} />}
        <span className="p-nodeglyph" style={{ color: health(node).color, background: `color-mix(in srgb, ${health(node).color} 15%, transparent)` }}><Icon name="nodes" size={15} /></span>
        <div><button className="p-text-action p-name" onClick={() => setDetailKey(keyOf(node))}>{node.name || node.remark || node.guid}</button><Tooltip title={node.transitive ? t('pages.nodes.subNodeTip', { parent: parentName(node) }) : undefined}><div className="p-sub node-card-subtitle">{node.transitive ? `${t('pages.nodes.subNode')} · ${parentName(node)}` : node.remark || t('pages.nodes.panelVersion') + ' ' + (node.panelVersion || '—')}</div></Tooltip></div>
      </div>} extra={<Space size={6}>{latency(node)}<StatusChip status={health(node)} pulse={health(node).key === 'online'} /></Space>}>
        {!node.transitive && isPanelUpdateAvailable(props.latestVersion ?? '', node.panelVersion ?? '') && <div className="p-nodebanner"><Icon name="up" size={13} /><span>{t('pages.nodes.updateAvailable')}: <span className="p-mono">{props.latestVersion}</span></span><Button size="small" type="primary" ghost disabled={!node.enable || node.status !== 'online'} onClick={() => props.onUpdateNode(node)}>{t('update')}</Button></div>}
        <NodeResources node={node} />
        <NodeThroughput node={node} />
        <div className="p-nodestats">
          <Field label={t('clients')} value={<>{node.clientCount ?? '—'} <span className="p-sub">· {node.onlineCount ?? '—'} {t('online')}</span></>} />
          <Field label={t('telemetry.inboundTraffic')} value={nodeTraffic(node) == null ? '—' : SizeFormatter.sizeFormat(nodeTraffic(node)!)} />
          <Field label={t('pages.nodes.uptime')} value={nodeHasReadings(node) && node.uptimeSecs != null ? TimeFormatter.formatSecond(node.uptimeSecs) : '—'} />
          <Field label={t('pages.nodes.lastHeartbeat')} value={heartbeat(node)} />
        </div>
        {(node.configDirty || node.lastError || node.xrayError) && <div className="node-card-notice">{node.configDirty && <Tag color="warning">{t('telemetry.pendingSync')}</Tag>}<span className="p-sub">{node.lastError || node.xrayError}</span></div>}
        {inbounds(node)}
        <div className="p-nodefoot">{actions(node)}<span className="node-action-spacer" />
          <Button size="small" type="text" onClick={() => setDetailKey(keyOf(node))}>{t('pages.cores.columns.detail')}</Button>
        </div>
      </PanelCard>)}
      {!nodes.length && <section className="p-card"><div className="p-plan-empty">{props.loading ? t('loading') : t('noData')}</div></section>}
    </div>}
    <Modal open={!!detail} className="node-detail-modal" title={detail && <Space wrap><span className="p-nodeglyph"><Icon name="nodes" size={15} /></span>{detail.name}<StatusChip status={health(detail)} />{latency(detail)}</Space>} width={720} destroyOnHidden onCancel={() => setDetailKey(null)}
      footer={<div className="p-nodefoot">{detail && actions(detail)}<span className="node-action-spacer" /><Button onClick={() => setDetailKey(null)}>{t('close')}</Button></div>}>
      {detail && <div className="node-detail-body">
        {detail.transitive && <span className="p-sub">{t('pages.nodes.subNodeTip', { parent: parentName(detail) })}</span>}
        <div className="p-card node-detail-kv">
          <Field label={t('pages.nodes.address')} value={address(detail)} />
          <Field label={t('pages.nodes.xrayVersion')} value={detail.xrayVersion || '—'} />
          <Field label={t('pages.nodes.panelVersion')} value={detail.panelVersion || '—'} />
          <Field label={t('pages.nodes.uptime')} value={nodeHasReadings(detail) && detail.uptimeSecs != null ? TimeFormatter.formatSecond(detail.uptimeSecs) : '—'} />
        </div>
        <PanelCard kicker={t('designParityNodes.resources', { defaultValue: 'Resources' })} pad={0}>
          <NodeResources node={detail} />
          <div className="node-detail-meta"><Field label={t('pages.nodes.lastHeartbeat')} value={heartbeat(detail)} /><Field label={t('pages.nodes.inboundSyncMode')} value={detail.transitive ? t('pages.nodes.subNode') : t(detail.inboundSyncMode === 'selected' ? 'pages.nodes.selectedInbounds' : 'pages.nodes.allInbounds')} />
            {detail.configDirty && <Tag color="warning">{t('telemetry.pendingSync')}</Tag>}
            {(detail.lastError || detail.xrayError) && <span className="p-sub">{detail.lastError || detail.xrayError}</span>}
          </div>
        </PanelCard>
        <PanelCard kicker={t('designParityNodes.history', { defaultValue: 'Resource and throughput history' })} pad={12}>{detail.transitive ? <span className="p-sub">{t('nodesFidelity.transitiveHistory')}</span> : <NodeHistoryPanel node={detail} />}</PanelCard>
        <div className="p-card node-detail-kv">
          {[['clients', detail.clientCount], ['online', detail.onlineCount], ['subscription.active', detail.activeCount], ['disabled', detail.disabledCount], ['depleted', detail.depletedCount], ['menu.inbounds', detail.inboundCount]].map(([key, value]) => <Field key={key} label={t(String(key))} value={value ?? '—'} />)}
          <Field label={t('telemetry.inboundTraffic')} value={nodeTraffic(detail) == null ? '—' : SizeFormatter.sizeFormat(nodeTraffic(detail)!)} />
        </div>
        <PanelCard pad={0} extra={<Button type="text" size="small" onClick={() => { setDetailKey(null); navigate('/inbounds'); }}>{t('menu.inbounds')}</Button>}>{inbounds(detail)}</PanelCard>
      </div>}
    </Modal>
  </div>;
}
