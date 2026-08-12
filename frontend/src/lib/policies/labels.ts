import { SizeFormatter } from '@/utils';
import { RATE_UNITS, splitBps, type PolicyTier } from '@/schemas/api/policy';

type Translate = (key: string, options?: Record<string, string | number>) => string;

/*
Every number a plan carries has a meaning at zero, and none of them is "zero".
These are the only place that decision is spelled, so a rate can never reach a
screen as a bare 0 the operator has to guess at.
*/

export function formatRate(bps: number, t: Translate): string {
  if (!Number.isFinite(bps) || bps <= 0) return t('pages.policies.unlimited');
  const { value, unit } = splitBps(bps);
  const label = RATE_UNITS.find((u) => u.key === unit)?.label ?? 'Mbps';
  return `${value} ${label}`;
}

export function formatThreshold(bytes: number, t: Translate): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return t('pages.policies.fromStart');
  return t('pages.policies.fromUsage', { used: SizeFormatter.sizeFormat(bytes) });
}

/* An IP limit of zero is unlimited, which is the opposite of what a bare 0
   reads as. Disabled is a third state and never the same sentence. */
export function formatIpLimit(limit: number, t: Translate): string {
  if (!Number.isFinite(limit) || limit <= 0) return t('pages.policies.unlimited');
  return t('pages.policies.ipLimitValue', { count: limit });
}

/* The ladder in one line per rung, in evaluation order. */
export function describeTier(tier: PolicyTier, t: Translate): string {
  const up = formatRate(tier.upBps, t);
  const down = formatRate(tier.downBps, t);
  return `${formatThreshold(tier.fromBytes, t)} — ↑ ${up} / ↓ ${down}`;
}
