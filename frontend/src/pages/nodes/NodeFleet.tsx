import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Checkbox, Input, Modal, Segmented, Space, Switch, Tag } from 'antd';
import { useNavigate } from 'react-router';
import { PageHeader, PanelCard, Gauge, Field, StatusChip } from '@/components/ui/panel';
import { Icon } from '@/components/ui/Icon';
import { TimeFormatter, SizeFormatter } from '@/utils';
import { nodeHasReadings, nodeTraffic } from './telemetry';
import NodeList, { type NodeListProps } from './NodeList';
import NodeHistoryPanel from './NodeHistoryPanel';
import NodeThroughput from './NodeThroughput';
import './NodeFleet.css';

export default function NodeFleet(props: NodeListProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [view, setView] = useState('cards');
  const [search, setSearch] = useState('');
  const [detailKey, setDetailKey] = useState<string | null>(null);
  const keyOf = (n: NodeListProps['nodes'][number]) => n.transitive ? `t-${n.guid}` : String(n.id);
  const detail = props.nodes.find((n) => keyOf(n) === detailKey);
  const nodes = props.nodes.filter((n) => `${n.name} ${n.address} ${n.remark}`.toLowerCase().includes(search.toLowerCase()));
  const health = (n: NodeListProps['nodes'][number]) => {
    const reachable = n.status === 'online';
    const coreFailed = reachable && ['stop', 'error', 'stopped', 'failed'].includes(n.xrayState ?? '');
    return { key: coreFailed ? 'failed' : reachable ? 'online' : 'offline', label: coreFailed ? t('pages.cores.state.failed') : reachable ? t('online') : t('pages.clients.offline'),
      color: coreFailed ? 'var(--p-warning)' : reachable ? 'var(--p-success)' : 'var(--p-error)' };
  };
  return <div className="p-network-page node-fleet">
    <div className="p-toolbar">
      <Space wrap><Input.Search value={search} onChange={(e) => setSearch(e.target.value)} allowClear
        aria-label={t('design.searchNodes')} placeholder={t('design.searchNodes')} />
        <Segmented aria-label={t('nodesFidelity.view')} value={view} onChange={setView} options={[{ value: 'cards', label: t('design.cardView') }, { value: 'table', label: t('design.tableView') }]} /></Space>
      {view === 'cards' && <Space wrap><Button onClick={props.onMtls}>{t('pages.nodes.mtls.title')}</Button>
        {!!props.selectedIds.length && <Button onClick={props.onUpdateSelected}>{t('pages.nodes.updateSelected', { count: props.selectedIds.length })}</Button>}
        <Button type="primary" icon={<Icon name="plus" />} onClick={props.onAdd}>{t('pages.nodes.addNode')}</Button></Space>}
    </div>
    {view === 'table' ? <NodeList {...props} nodes={nodes} isMobile={false} /> : <div className="p-groupgrid">
      {nodes.map((n) => <PanelCard key={keyOf(n)} pad={0} title={<Space>
        {!n.transitive && <Checkbox checked={props.selectedIds.includes(n.id)} disabled={!n.enable || n.status !== 'online'} aria-label={n.name || String(n.id)}
          onChange={(e) => props.onSelectionChange(e.target.checked ? [...props.selectedIds, n.id] : props.selectedIds.filter((id) => id !== n.id))} />}
        <button className="p-text-action p-name" onClick={() => setDetailKey(keyOf(n))}>{n.name || n.remark}</button>
      </Space>} extra={<StatusChip status={health(n)} />}>
        <div className="p-noderings">
          {[{ key: 'cpu', label: t('pages.nodes.cpu'), value: n.cpuPct, color: '#4c8dff' },
            { key: 'memory', label: t('pages.nodes.mem'), value: n.memPct, color: '#5ac8b0' },
            { key: 'disk', label: t('nodesFidelity.disk'), value: undefined, color: '#c79a4b' },
          ].map(resource => <div className="node-resource" key={resource.key} aria-label={`${resource.label}: ${!nodeHasReadings(n) || resource.value == null ? t('nodesFidelity.notReported') : `${resource.value}%`}`}>
            <Gauge value={resource.value ?? 0} muted={!nodeHasReadings(n) || resource.value == null} tone={resource.color} size={52} thick={4} />
            <span className="node-resource-label">{resource.label}</span>
            {resource.key === 'disk' && <span className="node-resource-note">{t('nodesFidelity.notReported')}</span>}
          </div>)}
        </div>
        <NodeThroughput node={n} />
        <div className="p-groupbody">
          <div className="p-detail-grid"><Field label={t('pages.nodes.avgLatency')} value={!nodeHasReadings(n) || n.latencyMs == null ? '—' : `${n.latencyMs} ms`} />
            <Field label={t('clients')} value={n.clientCount ?? '—'} /><Field label={t('menu.inbounds')} value={n.inboundCount ?? '—'} /></div>
          <div className="p-detail-grid"><Field label={t('online')} value={n.onlineCount ?? '—'} />
            <Field label={t('telemetry.inboundTraffic')} value={nodeTraffic(n) == null ? '—' : SizeFormatter.sizeFormat(nodeTraffic(n)!)} /></div>
          {n.configDirty && <Tag color="warning">{t('telemetry.pendingSync')}</Tag>}
          <span className="p-sub p-mono">{n.xrayVersion || '—'} · {n.uptimeSecs ? TimeFormatter.formatSecond(n.uptimeSecs) : '—'}</span>
          {(n.lastError || n.xrayError) && <span className="p-sub">{n.lastError || n.xrayError}</span>}
          <Space size={4} wrap>{(n.servedInbounds ?? []).map((ib) => <Tag key={ib.id}>{ib.tag} · {ib.protocol}</Tag>)}</Space>
        </div>
        <div className="p-nodefoot" style={{ padding: 12, borderTop: '1px solid var(--p-border)', display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <Button size="small" onClick={() => setDetailKey(keyOf(n))}>{t('pages.cores.columns.detail')}</Button>
          {n.transitive ? <Tag>{t('pages.nodes.subNode')}</Tag> : <>
            <Button size="small" onClick={() => props.onProbe(n)}>{t('check')}</Button>
            <Button size="small" onClick={() => props.onEdit(n)}>{t('edit')}</Button>
            {n.enable && n.status === 'online' && <Button size="small" onClick={() => props.onUpdateNode(n)}>{t('pages.nodes.updatePanel')}</Button>}
            <Button size="small" danger onClick={() => props.onDelete(n)}>{t('delete')}</Button>
            <Switch size="small" checked={!!n.enable} aria-label={`${t('enable')}: ${n.name}`} onChange={(v) => props.onToggleEnable(n, v)} />
          </>}
        </div>
      </PanelCard>)}
      {!nodes.length && <div className="p-plan-empty">{props.loading ? t('loading') : t('noData')}</div>}
    </div>}
    <Modal open={!!detail} title={detail?.name} width={900} footer={null} onCancel={() => setDetailKey(null)}>
      {detail && <>
        <PageHeader title={detail.name ?? ''} sub={`${detail.address ?? ''}:${detail.port ?? ''}`}
          actions={<Button onClick={() => { setDetailKey(null); navigate('/inbounds'); }}>{t('menu.inbounds')}</Button>} />
        {detail.transitive ? <Tag>{t('pages.nodes.subNode')}</Tag> : <NodeHistoryPanel node={detail} />}
      </>}
    </Modal>
  </div>;
}
