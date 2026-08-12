import { describe, it, expect } from 'vitest';

import {
  ONE_GB_BYTES,
  PolicyFormSchema,
  bytesToGB,
  emptyTierRow,
  gbToBytes,
  joinBps,
  parseTiers,
  splitBps,
  tierRowsFromWire,
  tierRowsToWire,
  type PolicyTier,
} from '@/schemas/api/policy';

/* The brief's own ladder: unlimited to 50 GB, then 10 Mbps, then 2 Mbps. */
const LADDER: PolicyTier[] = [
  { fromBytes: 0, upBps: 0, downBps: 0 },
  { fromBytes: 53687091200, upBps: 10000000, downBps: 10000000 },
  { fromBytes: 107374182400, upBps: 2000000, downBps: 2000000 },
];

describe('policy units', () => {
  /* A quota is binary and a rate is decimal. Reading one with the other's
     ladder misprices a threshold by 7% and a rate by a factor of eight. */
  it('a threshold is binary bytes, like every other quota in the panel', () => {
    expect(ONE_GB_BYTES).toBe(1024 ** 3);
    expect(gbToBytes(50)).toBe(53687091200);
    expect(bytesToGB(53687091200)).toBe(50);
    expect(bytesToGB(107374182400)).toBe(100);
  });

  it('a rate is decimal bits per second', () => {
    expect(joinBps(10, 'mbps')).toBe(10000000);
    expect(joinBps(2, 'mbps')).toBe(2000000);
    expect(joinBps(512, 'kbps')).toBe(512000);
    expect(joinBps(1, 'gbps')).toBe(1000000000);
  });

  const splits: { bps: number; value: number; unit: string }[] = [
    { bps: 10000000, value: 10, unit: 'mbps' },
    { bps: 2000000, value: 2, unit: 'mbps' },
    { bps: 1000000000, value: 1, unit: 'gbps' },
    { bps: 512000, value: 512, unit: 'kbps' },
    { bps: 1500000, value: 1500, unit: 'kbps' },
    { bps: 12345678, value: 12345.678, unit: 'kbps' },
  ];
  for (const s of splits) {
    it(`${s.bps} bps reads back as ${s.value} ${s.unit}`, () => {
      const got = splitBps(s.bps);
      expect(got).toEqual({ value: s.value, unit: s.unit });
      expect(joinBps(got.value, got.unit)).toBe(s.bps);
    });
  }

  /* Zero is unlimited on the wire, so it must never surface as a rate the
     operator could read as "capped at nothing". */
  it('an unlimited direction carries no rate at all', () => {
    expect(splitBps(0)).toEqual({ value: 0, unit: 'mbps' });
    expect(joinBps(0, 'mbps')).toBe(0);
  });
});

describe('tier rows', () => {
  it('round-trips the brief ladder through the editor unchanged', () => {
    expect(tierRowsToWire(tierRowsFromWire(LADDER))).toEqual(LADDER);
  });

  it('unlimited is a state on the row, never a zero the operator has to decode', () => {
    const rows = tierRowsFromWire(LADDER);
    expect(rows[0].upLimited).toBe(false);
    expect(rows[0].downLimited).toBe(false);
    expect(rows[1].upLimited).toBe(true);
    expect(rows[1].upValue).toBe(10);
    expect(rows[1].upUnit).toBe('mbps');
  });

  it('turning a direction unlimited drops its rate rather than keeping a stale one', () => {
    const rows = tierRowsFromWire(LADDER);
    rows[1].upLimited = false;
    expect(tierRowsToWire(rows)[1]).toEqual({ fromBytes: 53687091200, upBps: 0, downBps: 10000000 });
  });

  it('an unsorted ladder is stored ascending, the order the panel evaluates in', () => {
    const rows = tierRowsFromWire([LADDER[2], LADDER[0], LADDER[1]]);
    expect(tierRowsToWire(rows).map((t) => t.fromBytes)).toEqual([0, 53687091200, 107374182400]);
  });

  it('an asymmetric plan keeps the two directions independent', () => {
    const rows = [{ ...emptyTierRow(0), downLimited: true, downValue: 20, downUnit: 'mbps' as const }];
    expect(tierRowsToWire(rows)).toEqual([{ fromBytes: 0, upBps: 0, downBps: 20000000 }]);
  });
});

describe('parseTiers', () => {
  const cases: { name: string; raw: unknown; want: PolicyTier[] }[] = [
    { name: 'the array the API publishes', raw: LADDER, want: LADDER },
    { name: 'the JSON text the column stores', raw: JSON.stringify(LADDER), want: LADDER },
    { name: 'an empty ladder', raw: [], want: [] },
    { name: 'null, which is how an empty column marshals', raw: null, want: [] },
    { name: 'an empty string', raw: '', want: [] },
    { name: 'unparseable text never throttles', raw: '{oops', want: [] },
    { name: 'a ladder of the wrong shape never throttles', raw: [{ fromBytes: 'lots' }], want: [] },
  ];
  for (const c of cases) {
    it(c.name, () => { expect(parseTiers(c.raw)).toEqual(c.want); });
  }
});

describe('PolicyFormSchema', () => {
  function form(tiers: ReturnType<typeof emptyTierRow>[]) {
    return { id: 0, name: 'fair use', tiers };
  }

  it('accepts the brief ladder', () => {
    expect(PolicyFormSchema.safeParse(form(tierRowsFromWire(LADDER))).success).toBe(true);
  });

  it('refuses a nameless plan', () => {
    const result = PolicyFormSchema.safeParse({ ...form([]), name: '  ' });
    expect(result.success).toBe(false);
    expect(result.error?.issues[0].message).toBe('pages.policies.nameRequired');
  });

  /* The backend keeps the first of two rows at one threshold, so a form that
     let both through would silently lose whichever the operator meant. */
  it('refuses two tiers starting at the same usage', () => {
    const result = PolicyFormSchema.safeParse(form([emptyTierRow(50), emptyTierRow(50)]));
    expect(result.success).toBe(false);
    expect(result.error?.issues[0].message).toBe('pages.policies.thresholdDuplicate');
    expect(result.error?.issues[0].path).toEqual(['tiers', 1, 'fromGB']);
  });

  it('refuses a limited direction with no rate, which would be unlimited in disguise', () => {
    const result = PolicyFormSchema.safeParse(form([{ ...emptyTierRow(0), downLimited: true, downValue: 0 }]));
    expect(result.success).toBe(false);
    expect(result.error?.issues[0].message).toBe('pages.policies.rateRequired');
  });

  it('accepts a ladder that starts above zero, because below it the client is unlimited', () => {
    expect(PolicyFormSchema.safeParse(form([emptyTierRow(50)])).success).toBe(true);
  });
});
