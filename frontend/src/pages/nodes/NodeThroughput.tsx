import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Button } from 'antd';
import { z } from 'zod';
import type { NodeRecord } from '@/schemas/node';
import { HttpUtil, SizeFormatter } from '@/utils';
import HistoryChart from '../index/HistoryChart';
import { nodeHasReadings } from './telemetry';

const PointsSchema = z.array(z.object({ t: z.number(), v: z.number() }));
const BUCKET_SECONDS = 60;
const POINT_COUNT = 30;

export default function NodeThroughput({ node }: { node: NodeRecord }) {
  const { t } = useTranslation();
  const host = useRef<HTMLElement>(null);
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    if (!host.current) return;
    if (typeof IntersectionObserver === 'undefined') { setVisible(true); return; }
    const observer = new IntersectionObserver(([entry]) => setVisible(entry.isIntersecting), { rootMargin: '120px' });
    observer.observe(host.current);
    return () => observer.disconnect();
  }, []);
  const available = !node.transitive && node.id > 0;
  const query = useQuery({
    queryKey: ['node-card-throughput', node.id],
    enabled: available && visible,
    staleTime: 15_000,
    refetchInterval: node.enable && node.status === 'online' ? 15_000 : false,
    refetchOnWindowFocus: false,
    retry: false,
    queryFn: async ({ signal }) => {
      const [up, down] = await Promise.all(['netUp', 'netDown'].map(async metric => {
        const result = await HttpUtil.get(`/panel/api/nodes/history/${node.id}/${metric}/${BUCKET_SECONDS}`, undefined, { silent: true, signal });
        if (!result?.success) throw new Error(result?.msg || t('telemetry.historyUnavailable'));
        return PointsSchema.parse(result.obj).sort((left, right) => left.t - right.t).slice(-POINT_COUNT);
      }));
      return { up, down };
    },
  });
  const end = Math.floor(Date.now() / 1000 / BUCKET_SECONDS) * BUCKET_SECONDS;
  const times = Array.from({ length: POINT_COUNT }, (_, index) => end - (POINT_COUNT - 1 - index) * BUCKET_SECONDS);
  const values = (points: z.infer<typeof PointsSchema> | undefined) => {
    const byTime = new Map(points?.map(point => [point.t, point.v]));
    return times.map(time => byTime.get(time) ?? Number.NaN);
  };
  const up = values(query.data?.up);
  const down = values(query.data?.down);
  const hasHistory = up.some(Number.isFinite) || down.some(Number.isFinite);
  const reading = (value: number | undefined) => nodeHasReadings(node) && value != null ? SizeFormatter.speedFormat(value) : '—';
  return <figure className="node-throughput" ref={host} aria-label={t('nodesFidelity.throughput')}>
    <figcaption>
      <span className="p-kicker">{t('nodesFidelity.throughput')}</span>
      <span className="node-throughput-rates">
        <span title={t('pages.index.upload')}><i className="node-throughput-up" />↑ {reading(node.netUp)}</span>
        <span title={t('pages.index.download')}><i className="node-throughput-down" />↓ {reading(node.netDown)}</span>
      </span>
    </figcaption>
    {!available ? <div className="node-throughput-empty">{t('nodesFidelity.transitiveHistory')}</div>
      : query.isError ? <div className="node-throughput-empty" role="status"><span>{t('telemetry.historyUnavailable')}</span><Button size="small" onClick={() => { void query.refetch(); }}>{t('refresh')}</Button></div>
        : hasHistory ? <HistoryChart compact height={72} times={times} format={SizeFormatter.speedFormat} series={[
          { name: t('pages.index.upload'), values: up, color: '#4c8dff' },
          { name: t('pages.index.download'), values: down, color: '#5ac8b0' },
        ]} /> : <div className="node-throughput-empty">{t(query.isPending ? 'loading' : 'telemetry.noReadings')}</div>}
  </figure>;
}
