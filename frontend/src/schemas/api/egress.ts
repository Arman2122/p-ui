import { z } from 'zod';

import { EgressSchema } from '@/generated/zod';
import type { Egress } from '@/generated/zod';

export type EgressRecord = Egress;

export const EgressListSchema = z.array(EgressSchema);

/* What stops this host carrying an egress. Refusals and notes are backend
   sentences naming a sysctl or a device, so they are rendered, never translated. */
export const EgressPreflightSchema = z.object({
  ok: z.boolean(),
  refusals: z.array(z.string()),
  notes: z.array(z.string()),
  /* The host-forwarding half on its own, because a wgkernel inbound needs it and
     has no egress whose report it could read it out of. Defaulted for old panels. */
  forwardingNotes: z.array(z.string()).default([]),
});
export type EgressPreflight = z.infer<typeof EgressPreflightSchema>;

/* xray-tun terminates an L3 inbound inside Xray; wg-client dials a provider and
   is where traffic actually leaves. A third type — openvpn, ikev2 — is one entry
   here and one driver in Go, which is the whole point of the split. */
export const EGRESS_TYPE = 'xray-tun';
export const EGRESS_TYPE_UPLINK = 'wg-client';
export const EGRESS_TYPES = [EGRESS_TYPE, EGRESS_TYPE_UPLINK] as const;
export type EgressType = (typeof EGRESS_TYPES)[number];

/* An uplink carries no target: it IS the destination. Its settings are the
   fields every WireGuard provider hands out, named as their .conf names them so
   an operator pasting from Surfshark or Mullvad recognises each one. */
export const UplinkSettingsSchema = z.object({
  privateKey: z.string().trim().default(''),
  address: z.array(z.string()).default([]),
  mtu: z.number().int().nonnegative().default(0),
  publicKey: z.string().trim().default(''),
  endpoint: z.string().trim().default(''),
  presharedKey: z.string().trim().default(''),
  keepAlive: z.number().int().nonnegative().default(0),
});
export type UplinkSettings = z.infer<typeof UplinkSettingsSchema>;

/* The plain object stays exported because per-field validation reads `.shape`,
   which the refined schema below does not carry. */
export const EgressFormFields = z.object({
  id: z.number().int().default(0),
  type: z.enum(EGRESS_TYPES).default(EGRESS_TYPE),
  remark: z.string().trim().max(256).default(''),
  target: z.string().trim().default(''),
  enable: z.boolean().default(true),
  privateKey: z.string().trim().default(''),
  address: z.string().trim().default(''),
  mtu: z.number().int().nonnegative().default(0),
  publicKey: z.string().trim().default(''),
  endpoint: z.string().trim().default(''),
  presharedKey: z.string().trim().default(''),
  keepAlive: z.number().int().nonnegative().default(0),
});

/* Which fields are required depends on the type, so it is refined rather than
   marked on the fields: an uplink has no target, and a front has no keys. */
export const EgressFormSchema = EgressFormFields.superRefine((value, ctx) => {
  const need = (field: 'target' | 'privateKey' | 'publicKey' | 'endpoint' | 'address', key: string) => {
    if (!value[field]) ctx.addIssue({ code: 'custom', path: [field], message: key });
  };
  if (value.type === EGRESS_TYPE) {
    need('target', 'pages.xray.egress.targetRequired');
    return;
  }
  need('privateKey', 'pages.xray.egress.privateKeyRequired');
  need('publicKey', 'pages.xray.egress.publicKeyRequired');
  need('endpoint', 'pages.xray.egress.endpointRequired');
  need('address', 'pages.xray.egress.addressRequired');
});
export type EgressFormValues = z.infer<typeof EgressFormFields>;

/* One address per line or comma-separated, because that is how a provider's
   .conf writes it and pasting it whole must work. */
export function splitAddresses(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((part) => part.trim())
    .filter(Boolean);
}

/* Everything the operator owns. The timestamps are the database's, so a write
   that carried them back would be claiming to set them. */
export type EgressPayload = Omit<Egress, 'createdAt' | 'updatedAt'>;

/* An uplink as the API takes it. Its credentials live in the settings JSON the
   driver reads, and it names no target because it IS the destination. */
export function uplinkPayload(form: EgressFormValues, remark: string): EgressPayload {
  return {
    id: form.id,
    type: EGRESS_TYPE_UPLINK,
    remark: remark.trim(),
    target: '',
    enable: form.enable,
    owner: 'operator',
    ingressInboundId: 0,
    settings: JSON.stringify({
      privateKey: form.privateKey,
      address: splitAddresses(form.address),
      mtu: form.mtu,
      publicKey: form.publicKey,
      endpoint: form.endpoint,
      presharedKey: form.presharedKey,
      keepAlive: form.keepAlive,
    }),
  };
}
