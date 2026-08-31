import { z } from 'zod';

import { EgressSchema } from '@/generated/zod';
import type { Egress } from '@/generated/zod';
import { AwgParamsSchema, type AwgParams } from '@/schemas/protocols/inbound/awgkernel';

export type EgressRecord = Egress;

export const EgressListSchema = z.array(EgressSchema);

/* An exit shares the outbound table, so it needs a key in the same space that
   cannot collide with an outbound's array index. Above every plausible index,
   and reversible, because the row's id is what the test endpoint is asked for. */
export const EXIT_KEY_BASE = 1_000_000;
export const exitKeyFor = (id: number): number => EXIT_KEY_BASE + id;
export const exitIdFromKey = (key: number): number => key - EXIT_KEY_BASE;
export const isExitKey = (key: number): boolean => key >= EXIT_KEY_BASE;

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
/* AmneziaWG dials with the same fields, but must be its own type: the type is
   what decides which module creates the device, and one dialled as wg-client
   would connect with no obfuscation while the row claims otherwise. */
export const EGRESS_TYPE_AWG_UPLINK = 'awg-client';
export const EGRESS_TYPES = [EGRESS_TYPE, EGRESS_TYPE_UPLINK, EGRESS_TYPE_AWG_UPLINK] as const;
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
  /* Only an awg-client carries this. Named here because zod strips what it does
     not name, and a row parsed then saved would lose its obfuscation in silence. */
  awg: AwgParamsSchema.optional(),
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
  /* Carried through the form as one object, because ExitFormModal re-parses the
     values through this schema before toPayload ever sees them. */
  awg: AwgParamsSchema.optional(),
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
export function uplinkPayload(form: EgressFormValues, remark: string, type: EgressType = EGRESS_TYPE_UPLINK): EgressPayload {
  return {
    id: form.id,
    type,
    remark: remark.trim(),
    target: '',
    enable: form.enable,
    owner: 'operator',
    /* An uplink selects no ingress. The scalar 0 that used to sit here binds to
       a non-nil *int on the server and lands in a column with a foreign key onto
       inbounds(id), which no row can ever have — so every create was refused. */
    ingressInboundId: null,
    settings: JSON.stringify({
      privateKey: form.privateKey,
      address: splitAddresses(form.address),
      mtu: form.mtu,
      publicKey: form.publicKey,
      endpoint: form.endpoint,
      presharedKey: form.presharedKey,
      keepAlive: form.keepAlive,
      ...awgSettings(form.awg),
    }),
  };
}

/* Emitted only once something is set: an empty block on a wg-client row would
   claim an obfuscation its driver never applies. Zero is unset here too, exactly
   as it is in internal/awg's own omitempty encoding. */
function awgSettings(awg: AwgParams | undefined): { awg?: AwgParams } {
  if (!awg) return {};
  const set = Object.values(awg).some((v) => v !== undefined && v !== '' && v !== false && v !== 0);
  return set ? { awg } : {};
}
