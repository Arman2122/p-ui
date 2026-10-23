import { ClientCreatePayloadSchema, FleetClientCreateResultSchema } from '@/generated/zod';

export const creationAdmin = { id: 1, username: 'admin', role: 'admin', home: '/', permissions: { plans: false, reset: false, extend: false, suspend: false, topUp: false } } as const;
export const creationRequest = ClientCreatePayloadSchema.parse({ client: { email: 'owned-create', subId: 'preserved-subscription', id: '01234567-89ab-4cde-8123-456789abcdef',
  password: 'shared-private-password', privateKey: 'private-key-material', comment: '', enable: true, expiryTime: 0, limitIp: 0, reset: 0, security: '', tgId: 0, totalGB: 1000 },
  inboundIds: [1, 2], externalLinks: [{ kind: 'subscription', value: 'https://backup.example/sub/private-token', remark: 'Backup' }],
  credentialBindings: [{ inboundId: 2, values: { password: 'scoped-private-password', tlsCryptV2: 'scoped-wrapped-key' } }] });
export const creationResult = FleetClientCreateResultSchema.parse({ actorId: 1, clientId: '18bcb096-2b55-4c95-86f0-2d61900441ae', panelClientId: 8, revision: 1, pending: true,
  nodeDerived: [{ inboundId: 2, fields: ['tlsCryptV2'] }] });
export const creationOperationOne = 'b4e313bc-617b-4c88-a879-c182fbd0a3c9';
export const creationOperationTwo = '9ecb71ea-c624-4e90-b088-5cb1e1b4d3d0';
