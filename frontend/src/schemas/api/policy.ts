import { z } from 'zod';

import { EnforcedLimitsSchema, PolicySchema } from '@/generated/zod';
import type { EnforcedLimits, Policy } from '@/generated/zod';

export type PolicyRecord = Policy;
export type EnforcedRecord = EnforcedLimits;

export const PolicyListSchema = z.array(PolicySchema);
export { EnforcedLimitsSchema };

/* One rung of the ladder. fromBytes is binary bytes, like every quota in the
   panel; upBps and downBps are decimal bits per second, like every rate. */
export const PolicyTierSchema = z.object({
  fromBytes: z.number().int().min(0),
  upBps: z.number().int().min(0),
  downBps: z.number().int().min(0),
});
export type PolicyTier = z.infer<typeof PolicyTierSchema>;

/* Bytes stay binary (50 GB is 50 x 1024^3, the figure a quota already uses) and
   rates stay decimal (10 Mbps is 10,000,000 bits). Unifying them would misprice
   one of the two by 7% or by a factor of eight. */
export const ONE_GB_BYTES = 1024 ** 3;

/* The labels are the universal abbreviations and stay Latin in every locale,
   so they are rendered as identifiers rather than translated. */
export const RATE_UNITS = [
  { key: 'gbps', bps: 1_000_000_000, label: 'Gbps' },
  { key: 'mbps', bps: 1_000_000, label: 'Mbps' },
  { key: 'kbps', bps: 1_000, label: 'Kbps' },
] as const;
export type RateUnitKey = (typeof RATE_UNITS)[number]['key'];
export const RATE_UNIT_KEYS = RATE_UNITS.map((u) => u.key) as [RateUnitKey, ...RateUnitKey[]];

function unitBps(key: RateUnitKey): number {
  return RATE_UNITS.find((u) => u.key === key)?.bps ?? 1_000_000;
}

/* The largest unit that divides the rate exactly, so 10,000,000 reads back as
   the "10 Mbps" it was typed as and never as 10000 kbps. */
export function splitBps(bps: number): { value: number; unit: RateUnitKey } {
  const safe = Number.isFinite(bps) && bps > 0 ? Math.round(bps) : 0;
  if (safe === 0) return { value: 0, unit: 'mbps' };
  for (const unit of RATE_UNITS) {
    if (safe >= unit.bps && safe % unit.bps === 0) return { value: safe / unit.bps, unit: unit.key };
  }
  return { value: Number((safe / 1_000).toFixed(3)), unit: 'kbps' };
}

export function joinBps(value: number, unit: RateUnitKey): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.round(value * unitBps(unit));
}

export function bytesToGB(bytes: number): number {
  if (!Number.isFinite(bytes) || bytes <= 0) return 0;
  return Number((bytes / ONE_GB_BYTES).toFixed(4));
}

export function gbToBytes(gb: number): number {
  if (!Number.isFinite(gb) || gb <= 0) return 0;
  return Math.round(gb * ONE_GB_BYTES);
}

/* The column is JSON text, so a plan read from an older panel can still arrive
   as a string. Anything unreadable is an empty ladder, which never throttles. */
export function parseTiers(raw: unknown): PolicyTier[] {
  let value = raw;
  if (typeof value === 'string') {
    if (value.trim() === '') return [];
    try {
      value = JSON.parse(value);
    } catch {
      return [];
    }
  }
  const parsed = z.array(PolicyTierSchema).safeParse(value);
  return parsed.success ? parsed.data : [];
}

/* Ascending by threshold, the order the panel evaluates them in. */
export function sortTiers(tiers: PolicyTier[]): PolicyTier[] {
  return [...tiers].sort((a, b) => a.fromBytes - b.fromBytes);
}

/* One row of the ladder editor. The `limited` flags exist so unlimited is a
   state the operator picks, never a zero they have to know the meaning of. */
export const TierRowSchema = z.object({
  fromGB: z.number().min(0, 'pages.policies.thresholdInvalid'),
  upLimited: z.boolean(),
  upValue: z.number().min(0),
  upUnit: z.enum(RATE_UNIT_KEYS),
  downLimited: z.boolean(),
  downValue: z.number().min(0),
  downUnit: z.enum(RATE_UNIT_KEYS),
});
export type TierRow = z.infer<typeof TierRowSchema>;

export const PolicyFormSchema = z
  .object({
    id: z.number().int(),
    name: z.string().trim().min(1, 'pages.policies.nameRequired').max(128),
    tiers: z.array(TierRowSchema),
  })
  .superRefine((form, ctx) => {
    const seen = new Set<number>();
    form.tiers.forEach((tier, index) => {
      const bytes = gbToBytes(tier.fromGB);
      if (seen.has(bytes)) {
        ctx.addIssue({
          code: 'custom',
          path: ['tiers', index, 'fromGB'],
          message: 'pages.policies.thresholdDuplicate',
        });
      }
      seen.add(bytes);
      /* A limited direction with no rate is an unlimited one wearing a limit's
         label — the exact ambiguity the two-state control exists to remove. */
      if (tier.upLimited && joinBps(tier.upValue, tier.upUnit) <= 0) {
        ctx.addIssue({ code: 'custom', path: ['tiers', index, 'upValue'], message: 'pages.policies.rateRequired' });
      }
      if (tier.downLimited && joinBps(tier.downValue, tier.downUnit) <= 0) {
        ctx.addIssue({ code: 'custom', path: ['tiers', index, 'downValue'], message: 'pages.policies.rateRequired' });
      }
    });
  });
export type PolicyFormValues = z.infer<typeof PolicyFormSchema>;

export function emptyTierRow(fromGB = 0): TierRow {
  return {
    fromGB,
    upLimited: false,
    upValue: 0,
    upUnit: 'mbps',
    downLimited: false,
    downValue: 0,
    downUnit: 'mbps',
  };
}

export function tierRowsFromWire(tiers: PolicyTier[]): TierRow[] {
  return sortTiers(tiers).map((tier) => {
    const up = splitBps(tier.upBps);
    const down = splitBps(tier.downBps);
    return {
      fromGB: bytesToGB(tier.fromBytes),
      upLimited: tier.upBps > 0,
      upValue: up.value,
      upUnit: up.unit,
      downLimited: tier.downBps > 0,
      downValue: down.value,
      downUnit: down.unit,
    };
  });
}

export function tierRowsToWire(rows: TierRow[]): PolicyTier[] {
  return sortTiers(rows.map((row) => ({
    fromBytes: gbToBytes(row.fromGB),
    upBps: row.upLimited ? joinBps(row.upValue, row.upUnit) : 0,
    downBps: row.downLimited ? joinBps(row.downValue, row.downUnit) : 0,
  })));
}

/* Everything the operator owns. The timestamps are the database's, so a write
   that carried them back would be claiming to set them. */
export interface PolicyPayload {
  name: string;
  tiers: PolicyTier[];
}
