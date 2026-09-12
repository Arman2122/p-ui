import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import { HttpUtil, TimeFormatter } from '@/utils';
import type { Status } from '@/models/status';

const SERIES_KEYS = ['cpu', 'mem', 'swap', 'diskUsage', 'netUp', 'netDown', 'tcpCount', 'udpCount'] as const;
export type OverviewSeriesKey = (typeof SERIES_KEYS)[number];
export interface OverviewHistory {
 series: Record<OverviewSeriesKey, number[]>;
 labels: string[];
 times?: number[];
 error?: string;
}
export const HistoryWindowSchema = z.object({
 bucket: z.number().positive(), end: z.number(),
 series: z.record(z.string(), z.array(z.object({ t: z.number(), v: z.number() }))),
});

export function alignHistory(window: z.infer<typeof HistoryWindowSchema>): OverviewHistory {
 const times = Array.from({ length: 60 }, (_, i) => window.end - (59 - i) * window.bucket);
 const series = Object.fromEntries(SERIES_KEYS.map((key) => {
  const readings = new Map((window.series[key] ?? []).map((p) => [p.t, p.v]));
  return [key, times.map((ts) => readings.get(ts) ?? Number.NaN)];
 })) as Record<OverviewSeriesKey, number[]>;
 return { times, series, labels: times.map(TimeFormatter.formatClock) };
}
export function mean(values: number[]): number {
 const readings = values.filter(Number.isFinite);
 return readings.length ? readings.reduce((a, b) => a + b, 0) / readings.length : Number.NaN;
}
export function peak(values: number[]): number {
 const readings = values.filter(Number.isFinite);
 return readings.length ? Math.max(...readings) : Number.NaN;
}
export function useOverviewHistory(_status: Status, hasData: boolean, bucket = 60): OverviewHistory {
 const query = useQuery({
  queryKey: ['overview-history', bucket], enabled: hasData,
  queryFn: async () => {
   const msg = await HttpUtil.get(`/panel/api/server/historyWindow/${bucket}`, undefined, { silent: true });
   if (!msg?.success) throw new Error(msg?.msg || 'History unavailable');
   return HistoryWindowSchema.parse(msg.obj);
  }, refetchInterval: bucket === 2 ? 2000 : 15000,
 });
 return useMemo(() => query.data ? { ...alignHistory(query.data), error: query.error?.message } : {
  times: [], labels: [], error: query.error?.message,
  series: Object.fromEntries(SERIES_KEYS.map((key) => [key, [] as number[]])) as Record<OverviewSeriesKey, number[]>,
 }, [query.data, query.error]);
}
