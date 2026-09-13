import { useTranslation } from 'react-i18next';
import { Kicker, Meter } from '@/components/ui/panel';
import { GiB } from './api';
import type { ResellerView } from './schema';

export function Capacity({ label, used, limit, bytes = false }: { label: string; used: number; limit: number; bytes?: boolean }) {
  const { t } = useTranslation();
  const pct = limit ? Math.min(100, used / limit * 100) : 0;
  const format = (v: number) => bytes ? `${(v / GiB).toLocaleString(undefined, { maximumFractionDigits: 1 })} GiB` : v.toLocaleString();
  return <div className="p-capbar">
    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}><Kicker>{label}</Kicker><span className="p-sub">{limit ? `${Math.round(pct)}%` : t('resellers.unlimited')}</span></div>
    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 7 }}><strong>{format(used)}</strong><span className="p-sub">{limit ? format(limit) : '∞'}</span></div>
    <Meter pct={pct} height={4} tone={pct >= 90 ? 'var(--p-warning)' : 'var(--p-primary)'} />
  </div>;
}

export function ResellerRules({ reseller }: { reseller: ResellerView }) {
  const { t } = useTranslation();
  const a = reseller.account;
  return <div className="p-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
    <div><Kicker>{t('resellers.onDepletion')}</Kicker><strong>{t(`resellers.policy.${a.onDepletion}`)}</strong></div>
    <div><Kicker>{t('resellers.resetCycle')}</Kicker><strong>{t(`resellers.cycle.${a.resetCycle}`)}</strong>
      <div className="p-sub">{reseller.nextResetAt ? t('resellers.nextReset', { date: new Date(reseller.nextResetAt).toLocaleDateString() }) : t('resellers.noReset')}</div></div>
  </div>;
}

export default function ResellerBudget({ reseller }: { reseller: ResellerView }) {
  const { t } = useTranslation();
  const a = reseller.account;
  const days = a.expiresAt ? Math.ceil((a.expiresAt - Date.now()) / 86_400_000) : null;
  return <div style={{ display: 'grid', gap: 18 }}>
    <Capacity label={t('resellers.traffic')} used={reseller.trafficUsed} limit={a.trafficLimit} bytes />
    <Capacity label={t('resellers.clients')} used={reseller.clients} limit={a.clientCap} />
    <div><Kicker>{t('resellers.term')}</Kicker><strong>{days == null ? t('resellers.unlimited') : t('resellers.daysLeft', { days })}</strong><div className="p-sub">{a.expiresAt ? new Date(a.expiresAt).toLocaleDateString() : '—'}</div></div>
    <ResellerRules reseller={reseller} />
  </div>;
}
