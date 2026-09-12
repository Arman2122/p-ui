import { useTranslation } from 'react-i18next';
import { Alert, Segmented } from 'antd';
import { PanelCard, Field } from '@/components/ui/panel';
import type { Status } from '@/models/status';
import { SizeFormatter } from '@/utils';
import HistoryChart from './HistoryChart';
import { mean, peak, type OverviewHistory } from './useOverviewHistory';

export function HistoryWindowControl({ value, onChange }: { value: number; onChange: (v: number) => void }) {
  const { t } = useTranslation();
  return <Segmented size="small" value={value} onChange={onChange} aria-label={t('telemetry.window')}
    options={[{ value: 15, label: t('dashboardRepair.window15m') }, { value: 60, label: t('dashboardRepair.window1h') },
      { value: 1440, label: t('dashboardRepair.window24h') }, { value: 10080, label: t('dashboardRepair.window7d') }]} />;
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
  return <PanelCard kicker={t('dashboardRepair.throughput')} icon="chart" pad={0} className="ov-throughput-panel"
    extra={<div className="ov-throughput-legend"><span className="p-legend"><i style={{ background: '#4c8dff' }} />{t('pages.index.upload')}<b>{rate(status.unavailable.includes('netUp') ? Number.NaN : status.netIO.up)}</b></span>
      <span className="p-legend"><i style={{ background: '#5ac8b0' }} />{t('pages.index.download')}<b>{rate(status.unavailable.includes('netDown') ? Number.NaN : status.netIO.down)}</b></span></div>}>
    {history.error && <Alert type="warning" title={t('telemetry.historyUnavailable')} />}
    <div className="ov-throughput-chart">
      {!history.times?.length || ![...history.series.netUp, ...history.series.netDown].some(Number.isFinite) ? <div className="ov-chart-empty">{t('telemetry.noReadings')}</div> : <HistoryChart dashboard times={history.times} height={176} format={rate}
        series={[{ name: t('pages.index.upload'), values: history.series.netUp, color: '#4c8dff' },
          { name: t('pages.index.download'), values: history.series.netDown, color: '#5ac8b0' }]} />}
    </div>
    <div className="ov-throughput-footer">
      {cells.map(([label, value, sub]) => <Field key={label} label={label} value={value} sub={sub} />)}
    </div>
  </PanelCard>;
}

export function SessionsPanel({ status, history }: { status: Status; history: OverviewHistory }) {
  const { t } = useTranslation();
  const available = !status.unavailable.some((key) => key === 'tcpCount' || key === 'udpCount');
  const total = status.tcpCount + status.udpCount;
  const sockets = history.series.tcpCount.map((tcp, i) => Number.isFinite(tcp) && Number.isFinite(history.series.udpCount[i]) ? tcp + history.series.udpCount[i] : Number.NaN);
  return <PanelCard kicker={t('pages.index.liveSessions')} icon="hosts" pad={0} className="ov-sessions-panel">
    <div className="ov-session-current">
      <div><span className="p-stat">{available ? total : '—'}</span><span className="p-stat-u">{t('pages.index.sockets')}</span></div>
      <div className="ov-session-meter" aria-hidden="true">
        <i style={{ width: available && total > 0 ? `${status.tcpCount / total * 100}%` : 0, background: '#4c8dff' }} />
        <i style={{ width: available && total > 0 ? `${status.udpCount / total * 100}%` : 0, background: '#8c8f99' }} />
      </div>
      <div className="ov-session-legend"><span className="p-legend"><i style={{ background: '#4c8dff' }} />{t('pages.index.tcp')}<b>{status.unavailable.includes('tcpCount') ? '—' : status.tcpCount}</b></span>
        <span className="p-legend"><i style={{ background: '#8c8f99' }} />{t('pages.index.udp')}<b>{status.unavailable.includes('udpCount') ? '—' : status.udpCount}</b></span></div>
    </div>
    <div className="ov-session-chart"><HistoryChart dashboard times={history.times ?? []} height={96} compact format={(v) => String(Math.round(v))}
      series={[{ name: t('telemetry.hostSockets'), values: sockets, color: '#4c8dff' }]} /></div>
    <div className="ov-session-footer">
      <Field label={t('dashboardRepair.windowPeak')} value={Number.isFinite(peak(sockets)) ? Math.round(peak(sockets)) : '—'} sub={t('dashboardRepair.windowSamples')} />
      <Field label={t('dashboardRepair.handshakes')} value="—" sub={t('dashboardRepair.unreported')} />
    </div>
  </PanelCard>;
}
