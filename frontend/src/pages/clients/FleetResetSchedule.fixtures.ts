import { ResetScheduleViewSchema } from '@/generated/zod';
import { ClientRecordSchema } from '@/schemas/client';
import { fleetClientWire } from './ClientsFleet.fixtures';

export const resetClientId = 'e8c5a425-11b1-4f3a-9718-a60251d6810e';
export const resetOperationId = '535388fe-b795-4ff8-9318-bae7863753f7';
export const resetClient = ClientRecordSchema.parse({ ...fleetClientWire, traffic: { ...fleetClientWire.traffic,
  accounting: { ...fleetClientWire.traffic.accounting, clientId: resetClientId } } });
export const resetView = ResetScheduleViewSchema.parse({ clientId: resetClientId, revision: 7, inherited: true, cycle: 'weekly',
  anchorUnixMilli: Date.UTC(2026, 0, 31, 9, 30), nextDueUnixMilli: Date.UTC(2026, 1, 7, 9, 30), nextAttemptUnixMilli: Date.UTC(2026, 1, 7, 9, 30) });
export const resetAdmin = { id: 1, username: 'admin', role: 'admin', home: '/', permissions: { plans: false, reset: false, extend: false, suspend: false, topUp: false } } as const;
