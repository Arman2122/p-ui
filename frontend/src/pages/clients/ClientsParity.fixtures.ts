import { CoreViewSchema, EnforcedLimitsSchema } from '@/generated/zod';
import { ClientRecordSchema, ClientPageResponseSchema, InboundOptionsSchema } from '@/schemas/client';
import { PlanRecordSchema } from '@/schemas/api/policy';

export const parityInbounds = InboundOptionsSchema.parse([
  { id: 1, tag: 'GAME', remark: 'Game edge', protocol: 'vless', port: 443, enable: true, tlsFlowCapable: true },
  { id: 2, tag: 'backup', remark: 'Backup edge', protocol: 'trojan', port: 8443, enable: true },
]);
export const parityCores = [CoreViewSchema.parse({
  id: 'xray', titleKey: 'cores.xray.title', available: true, unavailable: '', kinds: ['vless', 'trojan'],
  caps: { onlineUsers: true, perUserStats: true, shareLink: true, userHotAdd: true },
  clientCredentials: { vless: ['uuid'], trojan: ['password'] }, applyDelaySeconds: {},
  exitKinds: [], portlessKinds: [], rateKinds: [], sessionKinds: ['vless', 'trojan'], shaping: {},
})];
export const parityPlans = [
  { id: 1, name: 'Standard 50', quotaBytes: 50 * 1024 ** 3, durationDays: 30, limitIp: 2, inboundIds: [1, 2], startOnFirstUse: true, autoRenewDays: 30, tiers: [{ fromBytes: 0, upBps: 5_000_000, downBps: 20_000_000 }] },
  { id: 2, name: 'Reseller private', ownerUserId: 19, quotaBytes: 100 * 1024 ** 3, durationDays: 90, tiers: [] },
  { id: 3, name: 'Retired plan', archived: true, tiers: [] },
].map((plan) => PlanRecordSchema.parse({ rev: 1, createdAt: 0, updatedAt: 0, ...plan }));
export const parityClient = ClientRecordSchema.parse({
  id: 1, email: 'roham', enable: true, subId: 'roham-subscription', uuid: '11112222-3333-4444-8555-666677778888',
  password: 'existing-trojan-password', flow: 'xtls-rprx-vision', totalGB: 11_505_016_832,
  expiryTime: -30 * 86400000, reset: 30, limitIp: 2, tgId: 812345, group: 'Friends',
  comment: 'Primary connection · keep credentials when editing', inboundIds: [1, 2],
  plan: { policyId: 1, name: 'Standard 50', unresolved: false, pending: false, overridden: true, overrideQuota: true },
  traffic: { up: 1024 ** 3, down: 3 * 1024 ** 3, lastOnline: 1789257600000 },
});
export const parityExternalLinks = [{ kind: 'subscription' as const, value: 'https://backup.example.net/sub/roham', remark: 'Backup subscription' }];
export const parityLink = 'vless://11112222-3333-4444-8555-666677778888@edge.example.net:443?security=tls&type=tcp#roham';
export const parityDefaults = { pageSize: 25, datepicker: 'gregorian', expireDiff: 3, trafficDiff: 2, tgBotEnable: true,
  subEnable: true, subURI: 'https://edge.example.net/sub/', subJsonEnable: true, subJsonURI: 'https://edge.example.net/json/' };
export const parityEnforced = EnforcedLimitsSchema.parse({ email: 'roham', policyId: 1, unresolved: false, usedBytes: 4 * 1024 ** 3,
  wantUpBps: 5_000_000, wantDownBps: 20_000_000, enforcedUpBps: 0, enforcedDownBps: 0, rateEnforced: false, shapeable: false });
export function parityClientPage() {
  const items = [parityClient,
    { ...parityClient, id: 2, email: 'office', group: 'Office', comment: 'Shared office connection', plan: null, totalGB: 0, expiryTime: 0, inboundIds: [1] },
    { ...parityClient, id: 3, email: 'travel', comment: 'Temporary access', enable: false, plan: null, totalGB: 2 * 1024 ** 3, disabledReason: 'quota' },
    { ...parityClient, id: 4, email: 'new-client', comment: '', plan: null, traffic: { up: 0, down: 0 }, totalGB: 0, expiryTime: 0, inboundIds: [] },
  ];
  return ClientPageResponseSchema.parse({ items, total: 4, filtered: 4, page: 1, pageSize: 25, groups: ['Friends', 'Office'],
    summary: { total: 4, active: 3, onlineCount: 1, depletedCount: 1, expiringCount: 0, deactiveCount: 0,
      online: ['roham'], depleted: ['travel'], expiring: [], deactive: [] } });
}
