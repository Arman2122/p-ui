import { ClientRecordSchema } from '@/schemas/client';

const GiB = 1024 ** 3;
const today = Math.floor(Date.now() / 86400000) * 86400;

export const fleetClientWire = {
  id: 71, email: 'fleet-pending-edit', enable: false, totalGB: GiB, expiryTime: 1,
  inboundIds: [], comment: 'Pending source edit',
  traffic: {
    up: GiB, down: 2 * GiB, total: 10 * GiB, expiryTime: 2208988800000, enable: true,
    accounting: {
      source: 'fleet', state: 'active', termId: 'current-term',
      measuredUploaded: 0, measuredDownloaded: 2 * GiB, openingUploaded: GiB, openingDownloaded: 0,
      reservedBytes: 4 * GiB, uncertainBytes: null, observedUnixMilli: 0, reportedUnixMilli: 0,
      historyBasis: 'reported', historyGranularity: 'day',
    },
  },
  history: [{ t: today - 86400, up: 0, down: GiB }, { t: today, up: 0, down: 0 }],
};

export const fleetClient = ClientRecordSchema.parse(fleetClientWire);
