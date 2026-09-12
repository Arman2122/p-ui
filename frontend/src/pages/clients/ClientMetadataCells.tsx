import { useTranslation } from 'react-i18next';
import { Tag, Tooltip } from 'antd';
import type { ClientRecord } from '@/schemas/client';
import { SizeFormatter } from '@/utils';
import HistoryChart from '../index/HistoryChart';

export function ClientPlanCell({ client }: { client: ClientRecord }) {
  const { t } = useTranslation();
  const plan = client.plan;
  if (!plan) return <span className="p-sub">{t('telemetry.customPlan')}</span>;
  return <div>
    <Tag color={plan.unresolved ? 'error' : 'blue'}>{plan.name || t(plan.unresolved ? 'telemetry.unresolvedPlan' : 'telemetry.inheritedPlan')}</Tag>
    {plan.pending && <div className="p-sub">{t('telemetry.planPending')}</div>}
    {plan.overridden && <div className="p-sub">{t('telemetry.planOverride')}</div>}
  </div>;
}

export function ClientTrendCell({ client, hint, wide = false, compact = false }: { client: ClientRecord; hint?: string; wide?: boolean; compact?: boolean }) {
  const { t } = useTranslation();
  const points = client.history ?? [];
  if (!points.length) return <span className="p-sub">{t('telemetry.noReadings')}</span>;
  const start = Math.floor(Date.now() / 86400000) * 86400 - 13 * 86400;
  const times = Array.from({ length: 14 }, (_, i) => start + i * 86400);
  const byTime = new Map(points.map((p) => [p.t, p.up + p.down]));
  return <Tooltip title={hint ?? t('telemetry.dailyTraffic')}>
    <div style={{ width: wide ? '100%' : compact ? 88 : 140 }}><HistoryChart times={times} compact height={compact ? 28 : 42} format={SizeFormatter.sizeFormat}
      series={[{ name: t('pages.clients.traffic'), color: '#5ac8b0', values: times.map((ts) => byTime.get(ts) ?? Number.NaN) }]} /></div>
  </Tooltip>;
}
