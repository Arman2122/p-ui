import { PlanRecordSchema, type PlanMemberPage } from '@/schemas/api/policy';
import type { InboundOption } from '@/schemas/client';

export const planParityInbounds: InboundOption[] = [
  { id: 11, remark: 'VLESS Reality', protocol: 'vless', port: 443 },
  { id: 12, remark: 'WireGuard Helsinki', protocol: 'wgkernel', port: 51820 },
  { id: 13, remark: 'Telegram FakeTLS', protocol: 'mtproto', port: 8443 },
];

export function planParityFixtures() {
  return [
    { id: 1, name: 'Pro 100', description: 'Everyday access with a shared selection of inbounds.', quotaBytes: 100 * 1024 ** 3, durationDays: 30, limitIp: 2, inboundIds: [11, 12, 13], assignedClients: 3, pendingClients: 1,
      usedBytes: 120 * 1024 ** 3, committedBytes: 300 * 1024 ** 3, startOnFirstUse: true, resetCycle: 'monthly', autoRenewDays: 30,
      tiers: [{ fromBytes: 0, upBps: 5_000_000, downBps: 20_000_000 }, { fromBytes: 80 * 1024 ** 3, upBps: 2_000_000, downBps: 5_000_000 }] },
    { id: 2, name: 'Starter 50', description: 'A smaller monthly allowance.', quotaBytes: 50 * 1024 ** 3, durationDays: 30, limitIp: 1, inboundIds: [11], assignedClients: 1, tiers: [] },
    { id: 3, name: 'Legacy', archived: true, quotaBytes: null, durationDays: null, limitIp: null, inboundIds: [], tiers: [] },
  ].map((plan) => PlanRecordSchema.parse({ rev: 1, createdAt: Date.UTC(2026, 7, 1), updatedAt: Date.UTC(2026, 8, 1), ...plan }));
}

export function planParityMembers(id: number): PlanMemberPage {
  return { total: id === 1 ? 3 : 0, items: id !== 1 ? [] : ['mina@example.test', 'ali@example.test', 'sara@example.test'].map((email, index) => ({
    email, group: index === 1 ? 'Family' : 'Friends', enable: index !== 2, usedBytes: [42, 72, 6][index] * 1024 ** 3,
    quotaBytes: 100 * 1024 ** 3, expiryTime: index === 1 ? -30 * 86400000 : Date.now() + 20 * 86400000,
    appliedRev: 1, pending: index === 1, overrideQuota: index === 2, overrideExpiry: false, overrideLimitIp: false, overrideInbounds: false,
  })) };
}
