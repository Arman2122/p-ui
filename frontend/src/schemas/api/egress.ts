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
/* A core-owned exit is stored under its kind: the SSH core dials the remote and
   fronts a loopback SOCKS port, and no plane driver is involved. */
export const EGRESS_TYPE_SSH = 'ssh';
/* Two more core-owned exits, each a client daemon the core runs whose tun the
   plane routes into; stored under the kind like ssh. */
export const EGRESS_TYPE_OPENVPN = 'openvpn';
export const EGRESS_TYPE_OCSERV = 'ocserv';
export const EGRESS_TYPES = [
  EGRESS_TYPE, EGRESS_TYPE_UPLINK, EGRESS_TYPE_AWG_UPLINK, EGRESS_TYPE_SSH, EGRESS_TYPE_OPENVPN, EGRESS_TYPE_OCSERV,
] as const;
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
  /* An SSH exit's own fields; a password or a key, and always the pin. */
  sshHost: z.string().trim().default(''),
  sshPort: z.number().int().min(1).max(65535).default(22),
  sshUser: z.string().trim().default(''),
  sshPassword: z.string().default(''),
  sshKey: z.string().default(''),
  sshFingerprint: z.string().trim().default(''),
  /* An OpenVPN exit: the provider's profile and the login it asks for. */
  ovpnProfile: z.string().default(''),
  ovpnUser: z.string().trim().default(''),
  ovpnPassword: z.string().default(''),
  /* An OpenConnect exit: the server, the login, and the pin of a self-signed one. */
  ocServer: z.string().trim().default(''),
  ocUser: z.string().trim().default(''),
  ocPassword: z.string().default(''),
  ocPin: z.string().trim().default(''),
  ocGroup: z.string().trim().default(''),
  ocCamouflageSecret: z.string().trim().default(''),
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
  if (value.type === EGRESS_TYPE_SSH) {
    const needSsh = (field: 'sshHost' | 'sshUser' | 'sshFingerprint', key: string) => {
      if (!value[field]) ctx.addIssue({ code: 'custom', path: [field], message: key });
    };
    needSsh('sshHost', 'pages.xray.egress.sshHostRequired');
    needSsh('sshUser', 'pages.xray.egress.sshUserRequired');
    needSsh('sshFingerprint', 'pages.xray.egress.sshFingerprintRequired');
    if (!value.sshPassword && !value.sshKey) {
      ctx.addIssue({ code: 'custom', path: ['sshPassword'], message: 'pages.xray.egress.sshAuthRequired' });
    }
    return;
  }
  if (value.type === EGRESS_TYPE_OPENVPN) {
    if (!value.ovpnProfile.trim()) {
      ctx.addIssue({ code: 'custom', path: ['ovpnProfile'], message: 'pages.xray.egress.ovpnProfileRequired' });
    }
    return;
  }
  if (value.type === EGRESS_TYPE_OCSERV) {
    const needOc = (field: 'ocServer' | 'ocUser' | 'ocPassword', key: string) => {
      if (!value[field]) ctx.addIssue({ code: 'custom', path: [field], message: key });
    };
    needOc('ocServer', 'pages.xray.egress.ocServerRequired');
    needOc('ocUser', 'pages.xray.egress.ocUserRequired');
    needOc('ocPassword', 'pages.xray.egress.ocPasswordRequired');
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

/* An SSH exit's stored settings, as the core reads them. */
export const SshExitSettingsSchema = z.object({
  host: z.string().trim().default(''),
  port: z.number().int().min(1).max(65535).default(22),
  username: z.string().trim().default(''),
  password: z.string().default(''),
  privateKey: z.string().default(''),
  hostKeyFingerprint: z.string().trim().default(''),
});

export function sshPayload(form: EgressFormValues, remark: string): EgressPayload {
  return {
    id: form.id,
    type: EGRESS_TYPE_SSH,
    remark: remark.trim(),
    target: '',
    enable: form.enable,
    owner: 'operator',
    ingressInboundId: null,
    settings: JSON.stringify({
      host: form.sshHost,
      port: form.sshPort,
      username: form.sshUser,
      password: form.sshPassword,
      privateKey: form.sshKey,
      hostKeyFingerprint: form.sshFingerprint,
    }),
  };
}

/* An OpenVPN exit's stored settings, as the core reads them. */
export const OpenvpnExitSettingsSchema = z.object({
  profile: z.string().default(''),
  username: z.string().trim().default(''),
  password: z.string().default(''),
});

export function openvpnPayload(form: EgressFormValues, remark: string): EgressPayload {
  return {
    id: form.id,
    type: EGRESS_TYPE_OPENVPN,
    remark: remark.trim(),
    target: '',
    enable: form.enable,
    owner: 'operator',
    ingressInboundId: null,
    settings: JSON.stringify({ profile: form.ovpnProfile, username: form.ovpnUser, password: form.ovpnPassword }),
  };
}

/* An OpenConnect exit's stored settings, as the core reads them. */
export const OcservExitSettingsSchema = z.object({
  server: z.string().trim().default(''),
  username: z.string().trim().default(''),
  password: z.string().default(''),
  serverCertPin: z.string().trim().default(''),
  group: z.string().trim().default(''),
  camouflageSecret: z.string().trim().default(''),
});

export function ocservPayload(form: EgressFormValues, remark: string): EgressPayload {
  return {
    id: form.id,
    type: EGRESS_TYPE_OCSERV,
    remark: remark.trim(),
    target: '',
    enable: form.enable,
    owner: 'operator',
    ingressInboundId: null,
    settings: JSON.stringify({
      server: form.ocServer,
      username: form.ocUser,
      password: form.ocPassword,
      serverCertPin: form.ocPin,
      group: form.ocGroup,
      camouflageSecret: form.ocCamouflageSecret,
    }),
  };
}
