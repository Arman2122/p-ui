import { describe, expect, it } from 'vitest';
import { ClientRecordSchema } from '@/schemas/client';
import { fleetClientWire } from '@/pages/clients/ClientsFleet.fixtures';
import { clientTrafficPolicy } from '@/lib/clients/traffic-display';

describe('fleet traffic API parsing', () => {
  it('preserves accounting evidence and authorized limits without overwriting editable source fields', () => {
    const parsed = ClientRecordSchema.parse(fleetClientWire);
    expect(parsed.traffic?.accounting).toEqual(fleetClientWire.traffic.accounting);
    expect(clientTrafficPolicy(parsed)).toEqual({ total: 10 * 1024 ** 3, expiryTime: 2208988800000, enabled: true });
    expect(parsed.totalGB).toBe(1024 ** 3);
    expect(parsed.expiryTime).toBe(1);
    expect(parsed.enable).toBe(false);
    expect(parsed.traffic?.accounting?.uncertainBytes).toBeNull();
  });

  it('keeps legacy display policy and rejects malformed accounting evidence', () => {
    const { accounting: _, ...traffic } = fleetClientWire.traffic;
    const legacy = ClientRecordSchema.parse({ ...fleetClientWire, traffic });
    expect(clientTrafficPolicy(legacy)).toEqual({ total: 1024 ** 3, expiryTime: 1, enabled: false });
    expect(ClientRecordSchema.safeParse({ ...fleetClientWire, traffic: { ...traffic, accounting: { ...fleetClientWire.traffic.accounting, reservedBytes: 'unknown' } } }).success).toBe(false);
  });
});
