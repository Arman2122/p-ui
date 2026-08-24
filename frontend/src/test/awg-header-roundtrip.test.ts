import { describe, expect, it } from 'vitest';

import { formValuesToWirePayload, rawInboundToFormValues } from '@/lib/xray/inbound-form-adapter';
import { InboundFormSchema } from '@/schemas/forms/inbound-form';
import { InboundSettingsSchema } from '@/schemas/protocols/inbound';

/*
Opening and saving an AmneziaWG inbound must not delete its headers.

AwgParamsSchema deliberately omitted h1..h4 and the timers, and zod strips what
it does not name. The form saves the PARSED object, so any value set through the
API was deleted by the first innocuous edit anyone made in the panel — a rename,
a port change. The device did not lose them at the same moment: an omitted
attribute means "leave it alone" to the kernel, so it kept the operator's custom
headers while every .conf the panel then handed out had none, and clients
silently fell back to WireGuard's 1/2/3/4. Every client of that inbound stops
connecting, and nothing anywhere says an edit did it.
*/
const row = {
  id: 7,
  protocol: 'awgkernel',
  remark: 'awg',
  port: 51820,
  listen: '',
  enable: true,
  settings: JSON.stringify({
    address: ['10.88.0.1/24'],
    secretKey: 'iD8eFDAR8KSbAAytwnhrggL20b49Kq88VJBVluGR83M=',
    clients: [],
    awg: {
      jc: 4, jmin: 40, jmax: 70,
      h1: '10-19', h2: '20-29', h3: '30-39', h4: '40-49',
      keepaliveTimeout: '15-25',
    },
  }),
  streamSettings: '',
  sniffing: '',
};

describe('an AmneziaWG inbound opened and saved in the panel', () => {
  it('keeps the headers and timers the form does not render', () => {
    // The modal's own sequence: read the row into form values, re-parse the
    // whole form, and save the PARSED object. The strip is in that middle step.
    const values = rawInboundToFormValues(row);
    const parsed = InboundFormSchema.safeParse(values);
    expect(parsed.success, JSON.stringify(parsed.error?.issues)).toBe(true);
    const payload = formValuesToWirePayload(parsed.data!);
    const awg = (JSON.parse(payload.settings) as { awg?: Record<string, unknown> }).awg ?? {};

    expect(awg.h1, 'h1 was dropped by the save; the device keeps it and every config handed out loses it').toBe('10-19');
    expect(awg.h2).toBe('20-29');
    expect(awg.h3).toBe('30-39');
    expect(awg.h4).toBe('40-49');
    expect(awg.keepaliveTimeout).toBe('15-25');
    // The fields the form does render are untouched by the same round trip.
    expect(awg.jc).toBe(4);
    expect(awg.jmax).toBe(70);
  });

  it('accepts a bare number for a header, which is the range [n, n]', () => {
    const parsed = InboundSettingsSchema.safeParse({
      protocol: 'awgkernel',
      settings: {
        address: ['10.88.0.1/24'],
        secretKey: 'iD8eFDAR8KSbAAytwnhrggL20b49Kq88VJBVluGR83M=',
        clients: [],
        awg: { h1: 10, h2: 20, h3: 30, h4: 40 },
      },
    });
    expect(parsed.success, JSON.stringify(parsed.error?.issues)).toBe(true);
  });
});
