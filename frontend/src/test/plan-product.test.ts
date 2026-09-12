import { describe, expect, it } from 'vitest';
import { ONE_GB_BYTES, PlanRecordSchema, PolicyFormSchema, PolicyPayloadSchema, planFormValues, tierRowsToWire } from '@/schemas/api/policy';

describe('plan template contract', () => {
  it('preserves unmanaged, unlimited, and finite template fields through the editor', () => {
    const plan = PlanRecordSchema.parse({
      id: 1, name: 'Family', description: 'Monthly plan', tiers: [{ fromBytes: 0, upBps: 12000000, downBps: 50000000 }],
      quotaBytes: null, durationDays: 0, limitIp: 2, inboundIds: [3, 8], resetCycle: 'weekly', rev: 1, createdAt: 0, updatedAt: 0,
    });
    const form = planFormValues(plan);
    const payload = PolicyPayloadSchema.parse({ ...form, tiers: tierRowsToWire(form.tiers) });
    expect(payload).toMatchObject({ quotaBytes: null, durationDays: 0, limitIp: 2, inboundIds: [3, 8], resetCycle: 'weekly', description: 'Monthly plan', tiers: plan.tiers });
    expect(payload).not.toHaveProperty('id');
    expect(payload).not.toHaveProperty('ownerUserId');
  });

  it('blocks renewal without a duration and continuation without quota or inbounds', () => {
    const draft = { ...planFormValues(), name: 'Invalid' };
    const invalid = PolicyFormSchema.safeParse({ ...draft, durationDays: null, autoRenewDays: 30, quotaBytes: 0, onDepletion: 'throttle' });
    expect(invalid.success).toBe(false);
    if (!invalid.success) expect(invalid.error.issues.map((issue) => issue.path)).toEqual([['durationDays'], ['onDepletion']]);
    expect(PolicyFormSchema.safeParse({ ...draft, quotaBytes: 10 * ONE_GB_BYTES, inboundIds: [1], onDepletion: 'notify' }).success).toBe(true);
  });
});
