import { Tag } from 'antd';
import { useTranslation } from 'react-i18next';
import type { PolicyRecord } from '@/schemas/api/policy';
import { parseTiers, sortTiers } from '@/schemas/api/policy';
import type { InboundOption } from '@/schemas/client';
import { formatInboundLabel } from '@/lib/inbounds/label';
import { formatRate } from '@/lib/policies/labels';
import { SizeFormatter } from '@/utils';

export default function ClientPlanSummary({ plan, inbounds }: { plan: PolicyRecord; inbounds: InboundOption[] }) {
  const { t } = useTranslation();
  const tier = sortTiers(parseTiers(plan.tiers)).find((row) => row.fromBytes === 0);
  const custom = t('telemetry.customPlan');
  const unlimited = t('unlimited');
  return <section className="client-plan-summary" aria-label={t('pages.policies.plan')}>
    <div className="client-plan-summary-grid">
      {[
        [t('pages.clients.traffic'), plan.quotaBytes == null ? custom : plan.quotaBytes ? SizeFormatter.sizeFormat(plan.quotaBytes) : unlimited],
        [t('pages.clients.duration'), plan.durationDays == null ? custom : plan.durationDays ? t('design.days', { count: plan.durationDays }) : unlimited],
        [t('pages.clients.speed'), `↓ ${formatRate(tier?.downBps ?? 0, t)} · ↑ ${formatRate(tier?.upBps ?? 0, t)}`],
        [t('pages.clients.limitIp'), plan.limitIp == null ? custom : plan.limitIp || unlimited],
      ].map(([label, value]) => <div key={label}>
        <div className="p-kicker">{label}</div><div className="client-plan-summary-value">{value}</div>
      </div>)}
    </div>
    <div className="client-plan-summary-inbounds">
      <div className="p-kicker">{t('pages.clients.attachedInbounds')}</div>
      <div>{plan.inboundIds.length ? plan.inboundIds.map((id) => {
        const inbound = inbounds.find((row) => row.id === id);
        return <Tag key={id} color="blue">{inbound ? formatInboundLabel(inbound.tag, inbound.remark) : `#${id}`}</Tag>;
      }) : <span className="p-sub">{custom}</span>}</div>
    </div>
  </section>;
}
