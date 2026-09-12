import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from 'antd';

import HistoryChart from './HistoryChart';
import { Icon } from '@/components/ui/Icon';
import { Gauge, Kicker } from '@/components/ui/panel';
import { CPUFormatter, SizeFormatter } from '@/utils';
import type { Status } from '@/models/status';
import { mean, peak, type OverviewHistory, type OverviewSeriesKey } from './useOverviewHistory';

export type VitalKey = 'cpu' | 'mem' | 'swap' | 'diskUsage';

/* Chart tones are deliberately fixed rather than themed: each vital keeps the same
   colour in light and dark so the four sparklines stay tellable apart. */
const TONE: Record<VitalKey, string> = {
  cpu: '#4c8dff',
  mem: '#5ac8b0',
  swap: '#8c8f99',
  diskUsage: '#c79a4b',
};

type Vital = {
  key: VitalKey;
  label: string;
  value: number;
  detail: string;
  foot: string;
  muted?: boolean;
  rows: [string, string][];
};

function pct(value: number): string {
  return Number.isFinite(value) ? `${value.toFixed(1)}%` : '—';
}

export default function VitalsBand({ status, history, open, onOpen, lead }: {
  status: Status;
  history: OverviewHistory;
  open: VitalKey | null;
  onOpen: (key: VitalKey | null) => void;
  lead?: React.ReactNode;
}) {
  const { t } = useTranslation();

  const vitals = useMemo<Vital[]>(() => {
    const series = (key: OverviewSeriesKey) => history.series[key] ?? [];
    const avgPeak = (key: OverviewSeriesKey): [string, string] => [pct(mean(series(key))), pct(peak(series(key)))];

    const [cpuAvg, cpuPeak] = avgPeak('cpu');
    const [memAvg, memPeak] = avgPeak('mem');
    const [swapAvg, swapPeak] = avgPeak('swap');
    const [diskAvg, diskPeak] = avgPeak('diskUsage');

    const memFree = Math.max(0, status.mem.total - status.mem.current);
    const diskFree = Math.max(0, status.disk.total - status.disk.current);
    const swapOff = status.swap.total <= 0;

    return [
      {
        key: 'cpu',
        label: t('pages.index.cpu'),
        value: status.cpu.percent,
        detail: `${CPUFormatter.cpuCoreFormat(status.cpuCores)} / ${status.logicalPro}T · ${CPUFormatter.cpuSpeedFormat(status.cpuSpeedMhz)}`,
        foot: `${t('pages.index.avg')} ${cpuAvg} · ${t('pages.index.peak')} ${cpuPeak}`,
        rows: [
          [t('pages.index.avg'), cpuAvg],
          [t('pages.index.peak'), cpuPeak],
          [t('pages.index.loadAverage'), status.loads.map((n) => n.toFixed(2)).join(' / ')],
          [t('pages.index.threadsLogical'), String(status.logicalPro)],
        ],
      },
      {
        key: 'mem',
        label: t('pages.index.memory'),
        value: status.mem.percent,
        detail: `${SizeFormatter.sizeFormat(status.mem.current)} / ${SizeFormatter.sizeFormat(status.mem.total)}`,
        foot: `${t('pages.index.avg')} ${memAvg} · ${SizeFormatter.sizeFormat(memFree)} ${t('pages.index.free')}`,
        rows: [
          [t('pages.index.avg'), memAvg],
          [t('pages.index.peak'), memPeak],
          [t('pages.index.free'), SizeFormatter.sizeFormat(memFree)],
          [t('pages.index.panelRss'), SizeFormatter.sizeFormat(status.appStats.mem)],
        ],
      },
      {
        key: 'swap',
        label: t('pages.index.swap'),
        value: status.swap.percent,
        muted: swapOff,
        detail: swapOff ? t('pages.index.swapOff') : `${SizeFormatter.sizeFormat(status.swap.current)} / ${SizeFormatter.sizeFormat(status.swap.total)}`,
        foot: swapOff ? t('pages.index.swapOffHint') : `${t('pages.index.avg')} ${swapAvg} · ${t('pages.index.peak')} ${swapPeak}`,
        rows: [
          [t('pages.index.avg'), swapAvg],
          [t('pages.index.peak'), swapPeak],
          [t('pages.index.size'), SizeFormatter.sizeFormat(status.swap.total)],
          [t('pages.index.used'), SizeFormatter.sizeFormat(status.swap.current)],
        ],
      },
      {
        key: 'diskUsage',
        label: t('pages.index.storage'),
        value: status.disk.percent,
        detail: `${SizeFormatter.sizeFormat(status.disk.current)} / ${SizeFormatter.sizeFormat(status.disk.total)}`,
        foot: `${SizeFormatter.sizeFormat(diskFree)} ${t('pages.index.free')}`,
        rows: [
          [t('pages.index.free'), SizeFormatter.sizeFormat(diskFree)],
          [t('pages.index.avg'), diskAvg],
          [t('pages.index.peak'), diskPeak],
          [t('pages.index.mount'), t('pages.index.rootFs')],
        ],
      },
    ];
  }, [status, history, t]);

  const current = vitals.find((v) => v.key === open);

  return (
    <section className="p-card">
      <div className="p-vitals">
        {lead}
        {vitals.map((v) => (
          <button
            type="button"
            key={v.key}
            className={`p-vital${open === v.key ? ' is-open' : ''}`}
            onClick={() => onOpen(open === v.key ? null : v.key)}
            aria-expanded={open === v.key}
            title={t('pages.index.showHistory', { metric: v.label })}
          >
            {status.unavailable.includes(v.key) ? <span className="p-stat" style={{ width: 56 }}>—</span> :
              <Gauge value={v.value} unit="%" tone={TONE[v.key]} muted={v.muted} size={56} thick={4.5} />}
            <span className="p-vital-t">
              <span className="p-vital-l">{v.label}</span>
              <span className="p-vital-d">{status.unavailable.includes(v.key) ? t('telemetry.noReadings') : v.detail}</span>
              <span className="p-vital-f">{v.foot}</span>
            </span>
          </button>
        ))}
      </div>
      {current && (
        <div className="p-vitals-x">
          <div style={{ flex: '1 1 380px', minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, marginBottom: 6 }}>
              <Kicker>{t('pages.index.lastSamples', { metric: current.label })}</Kicker>
              <Button type="text" size="small" onClick={() => onOpen(null)} aria-label={t('close')} icon={<Icon name="close" size={14} />} />
            </div>
            <HistoryChart times={history.times ?? []} height={160} format={pct}
              series={[{ name: current.label, values: history.series[current.key] ?? [], color: TONE[current.key] }]} />
          </div>
          <div style={{ flex: '0 1 220px', minWidth: 180 }}>
            {current.rows.map(([key, value]) => (
              <div className="p-kv" key={key}>
                <span className="p-kv-k">{key}</span>
                <span className="p-kv-v">{value}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}
