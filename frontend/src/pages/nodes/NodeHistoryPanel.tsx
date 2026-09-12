import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Segmented } from 'antd';
import { z } from 'zod';
import { HttpUtil, SizeFormatter } from '@/utils';
import HistoryChart from '../index/HistoryChart';
import './NodeHistoryPanel.css';

const PointsSchema = z.array(z.object({ t: z.number(), v: z.number() }));
const METRICS = ['cpu', 'mem', 'netUp', 'netDown'] as const;

export default function NodeHistoryPanel({ node, bucket: initialBucket = 60 }: { node: { id: number }; bucket?: number }) {
 const { t } = useTranslation();
 const metricLabel = (metric: string) => metric === 'netUp' ? t('pages.index.upload') : metric === 'netDown' ? t('pages.index.download') : t(`pages.nodes.${metric}`);
 const [bucket, setBucket] = useState(initialBucket);
 const query = useQuery({ queryKey: ['node-history-window', node.id, bucket], refetchInterval: 15000,
  queryFn: async () => {
   const readings = await Promise.all(METRICS.map(async (metric) => {
    const msg = await HttpUtil.get(`/panel/api/nodes/history/${node.id}/${metric}/${bucket}`, undefined, { silent: true });
    if (!msg?.success) throw new Error(msg?.msg || t('telemetry.historyUnavailable'));
    return [metric, PointsSchema.parse(msg.obj)] as const;
   }));
   return Object.fromEntries(readings);
  },
 });
 const end = Math.floor(Date.now() / 1000 / bucket) * bucket;
 const times = Array.from({ length: 60 }, (_, i) => end - (59 - i) * bucket);
 const values = (key: string) => {
  const points = new Map((query.data?.[key] ?? []).map((p) => [p.t, p.v]));
  return times.map((ts) => points.get(ts) ?? Number.NaN);
 };
 return <>
  <Segmented value={bucket} onChange={setBucket} aria-label={t('telemetry.window')}
   options={[{ value: 60, label: t('telemetry.hour') }, { value: 1440, label: t('telemetry.day') }, { value: 10080, label: t('telemetry.week') }]} />
  {query.error && <Alert type="error" title={t('telemetry.historyUnavailable')} />}
  <div className="node-history-panel">
   {METRICS.map((metric, i) => <div className="series" key={metric}>
    <div className="series-title">{metricLabel(metric)}</div>
    {!(query.data?.[metric]?.length) ? <div className="p-empty">{query.isFetching ? t('loading') : t('telemetry.noReadings')}</div> :
     <HistoryChart times={times} height={170} format={i < 2 ? (v) => `${v.toFixed(1)}%` : SizeFormatter.speedFormat}
      series={[{ name: metricLabel(metric), values: values(metric), color: i % 2 ? '#5ac8b0' : '#4c8dff' }]} />}
   </div>)}
  </div>
 </>;
}
