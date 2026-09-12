import { useTranslation } from 'react-i18next';
import { Alert, Segmented } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { PanelCard, Field } from '@/components/ui/panel';
import { StatStrip } from '@/components/ui';
import { ClientPageResponseSchema } from '@/schemas/client';
import type { Status } from '@/models/status';
import { HttpUtil, SizeFormatter } from '@/utils';
import HistoryChart from './HistoryChart';
import { mean, peak, type OverviewHistory } from './useOverviewHistory';

export function HistoryWindowControl({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  const { t } = useTranslation();
  return <Segmented size="small" value={value} onChange={onChange} aria-label={t('telemetry.window')}
    options={[{ value: 15, label: t('pages.index.minutesShort', { count: 15 }) }, { value: 60, label: t('telemetry.hour') },
      { value: 1440, label: t('telemetry.day') }, { value: 10080, label: t('telemetry.week') }]} />;
}

export function DashboardClientCounters() {
  const { t } = useTranslation();
  const query = useQuery({ queryKey: ['dashboard-client-summary'], refetchInterval: 15000,
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/clients/list/paged', { pageSize: 1 }, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Client counts unavailable');
      return ClientPageResponseSchema.parse(msg.obj).summary;
    } });
  const summary = query.data;
  return <>
    {query.error && <Alert type="warning" title={t('telemetry.countsUnavailable')} />}
    <StatStrip items={[
      { key: 'clients', label: t('clients'), value: summary?.total ?? '—' },
      { key: 'online', label: t('online'), value: summary?.onlineCount ?? '—', color: 'var(--p-success)' },
      { key: 'expiring', label: t('depletingSoon'), value: summary?.expiringCount ?? '—' },
      { key: 'depleted', label: t('depleted'), value: summary?.depletedCount ?? '—' },
    ]} />
  </>;
}

export function ThroughputPanel({ status, history }: { status: Status; history: OverviewHistory }) {
  const { t } = useTranslation();
  const rate = (v: number) => Number.isFinite(v) ? SizeFormatter.speedFormat(v) : '—';
  const cells = [
    [t('pages.index.sent'), status.unavailable.includes('netTraffic') ? '—' : SizeFormatter.sizeFormat(status.netTraffic.sent), t('telemetry.hostCounters')],
    [t('pages.index.received'), status.unavailable.includes('netTraffic') ? '—' : SizeFormatter.sizeFormat(status.netTraffic.recv), t('telemetry.hostCounters')],
    [t('pages.index.avgWindow'), `↑ ${rate(mean(history.series.netUp))}`, `↓ ${rate(mean(history.series.netDown))}`],
    [t('pages.index.peak'), rate(Math.max(peak(history.series.netUp), peak(history.series.netDown))), t('telemetry.sampledPeak')],
  ];
  return <PanelCard kicker={t('pages.index.overallSpeed')} icon="chart" pad={0} className="p-span2"
    extra={<span className="p-mono">↑ {rate(status.unavailable.includes('netUp') ? Number.NaN : status.netIO.up)} · ↓ {rate(status.unavailable.includes('netDown') ? Number.NaN : status.netIO.down)}</span>}>
    {history.error && <Alert type="warning" title={t('telemetry.historyUnavailable')} />}
    <div style={{ padding: '10px 6px' }}>
      {!history.times?.length || ![...history.series.netUp, ...history.series.netDown].some(Number.isFinite) ? <div className="p-empty">{t('telemetry.noReadings')}</div> : <HistoryChart times={history.times} height={210} format={rate}
        series={[{ name: t('pages.index.upload'), values: history.series.netUp, color: '#4c8dff' },
          { name: t('pages.index.download'), values: history.series.netDown, color: '#5ac8b0' }]} />}
    </div>
    <div className="p-kvgrid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(120px,1fr))', borderTop: '1px solid var(--p-border)' }}>
      {cells.map(([label, value, sub]) => <div key={label} style={{ padding: '11px 14px' }}><Field label={label} value={value} sub={sub} /></div>)}
    </div>
  </PanelCard>;
}

export function SessionsPanel({ status, history }: { status: Status; history: OverviewHistory }) {
  const { t } = useTranslation();
  return <PanelCard kicker={t('telemetry.hostSockets')} icon="hosts" pad={14}>
    <div className="p-stat">{status.unavailable.some((key) => key === 'tcpCount' || key === 'udpCount') ? '—' : status.tcpCount + status.udpCount}</div>
    <div className="p-sub">TCP {status.unavailable.includes('tcpCount') ? '—' : status.tcpCount} · UDP {status.unavailable.includes('udpCount') ? '—' : status.udpCount}</div>
    <HistoryChart times={history.times ?? []} height={140} compact format={(v) => String(Math.round(v))}
      series={[{ name: 'TCP', values: history.series.tcpCount, color: '#4c8dff' },
        { name: 'UDP', values: history.series.udpCount, color: '#8c8f99' }]} />
    <DashboardClientCounters />
  </PanelCard>;
}
