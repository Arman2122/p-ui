import type { ComponentType } from 'react';

import {
  EGRESS_TYPE_AWG_UPLINK,
  EGRESS_TYPE_OCSERV,
  EGRESS_TYPE_OPENVPN,
  EGRESS_TYPE_SSH,
  EGRESS_TYPE_UPLINK,
  EgressFormFields,
  OcservExitSettingsSchema,
  OpenvpnExitSettingsSchema,
  SshExitSettingsSchema,
  UplinkSettingsSchema,
  ocservPayload,
  openvpnPayload,
  sshPayload,
  uplinkPayload,
  type EgressFormValues,
  type EgressPayload,
  type EgressRecord,
} from '@/schemas/api/egress';

import AwgkernelExitFields from './awgkernel-fields';
import OcservExitFields from './ocserv-fields';
import OpenvpnExitFields from './openvpn-fields';
import SshExitFields from './ssh-fields';
import WgkernelExitFields from './wgkernel-fields';

/*
One module per kind a route can exit on.

The two vocabularies meet here and nowhere else: a CORE KIND is what the registry
declares and what the picker shows, an EGRESS TYPE is what the row is stored as
and which driver dials it. A core-owned exit (ssh) has no driver, so its type is
its kind. Before this, both were the WireGuard uplink's — so a
second core declaring an exit rendered WireGuard's fields and saved itself as a
wg-client, which the wrong driver would then try to dial.

A kind with no module here is offered but refused, with the reason shown. That is
deliberate: the registry is the backend's answer about what CAN exit, and this
build either has a form for it or honestly says it does not.
*/
export interface ExitKindModule {
  /* The egress row type this kind is stored as — the driver's name for it. */
  egressType: string;
  Fields: ComponentType;
  /* Form values → the row the API takes. */
  toPayload: (form: EgressFormValues, remark: string) => EgressPayload;
  /* An existing row → form values, for the edit form. */
  fromRecord: (row: EgressRecord) => EgressFormValues;
}

/* Shared by both uplink kinds: the stored settings are the same object, and an
   awg row is one that also carries `awg`. A wg row has none, so it reads back
   undefined and nothing changes for it. */
function uplinkFromRecord(row: EgressRecord): EgressFormValues {
  const base = EgressFormFields.parse({ remark: row.remark ?? '', enable: row.enable ?? true });
  if (!row.settings) return base;
  try {
    const parsed = UplinkSettingsSchema.safeParse(JSON.parse(row.settings));
    if (!parsed.success) return base;
    const s = parsed.data;
    return {
      ...base,
      id: row.id,
      privateKey: s.privateKey,
      address: s.address.join('\n'),
      mtu: s.mtu,
      publicKey: s.publicKey,
      endpoint: s.endpoint,
      presharedKey: s.presharedKey,
      keepAlive: s.keepAlive,
      awg: s.awg,
    };
  } catch {
    return base;
  }
}

function sshFromRecord(row: EgressRecord): EgressFormValues {
  const base = EgressFormFields.parse({ type: EGRESS_TYPE_SSH, remark: row.remark ?? '', enable: row.enable ?? true });
  if (!row.settings) return base;
  try {
    const parsed = SshExitSettingsSchema.safeParse(JSON.parse(row.settings));
    if (!parsed.success) return base;
    const s = parsed.data;
    return {
      ...base,
      id: row.id,
      sshHost: s.host,
      sshPort: s.port,
      sshUser: s.username,
      sshPassword: s.password,
      sshKey: s.privateKey,
      sshFingerprint: s.hostKeyFingerprint,
    };
  } catch {
    return base;
  }
}

function openvpnFromRecord(row: EgressRecord): EgressFormValues {
  const base = EgressFormFields.parse({ type: EGRESS_TYPE_OPENVPN, remark: row.remark ?? '', enable: row.enable ?? true });
  if (!row.settings) return base;
  try {
    const parsed = OpenvpnExitSettingsSchema.safeParse(JSON.parse(row.settings));
    if (!parsed.success) return base;
    return { ...base, id: row.id, ovpnProfile: parsed.data.profile, ovpnUser: parsed.data.username, ovpnPassword: parsed.data.password };
  } catch {
    return base;
  }
}

function ocservFromRecord(row: EgressRecord): EgressFormValues {
  const base = EgressFormFields.parse({ type: EGRESS_TYPE_OCSERV, remark: row.remark ?? '', enable: row.enable ?? true });
  if (!row.settings) return base;
  try {
    const parsed = OcservExitSettingsSchema.safeParse(JSON.parse(row.settings));
    if (!parsed.success) return base;
    const s = parsed.data;
    return { ...base, id: row.id, ocServer: s.server, ocUser: s.username, ocPassword: s.password, ocPin: s.serverCertPin, ocGroup: s.group, ocCamouflageSecret: s.camouflageSecret };
  } catch {
    return base;
  }
}

const MODULES: Record<string, ExitKindModule> = {
  ssh: {
    egressType: EGRESS_TYPE_SSH,
    Fields: SshExitFields,
    toPayload: sshPayload,
    fromRecord: sshFromRecord,
  },
  openvpn: {
    egressType: EGRESS_TYPE_OPENVPN,
    Fields: OpenvpnExitFields,
    toPayload: openvpnPayload,
    fromRecord: openvpnFromRecord,
  },
  ocserv: {
    egressType: EGRESS_TYPE_OCSERV,
    Fields: OcservExitFields,
    toPayload: ocservPayload,
    fromRecord: ocservFromRecord,
  },
  wgkernel: {
    egressType: EGRESS_TYPE_UPLINK,
    Fields: WgkernelExitFields,
    toPayload: uplinkPayload,
    fromRecord: uplinkFromRecord,
  },
  awgkernel: {
    egressType: EGRESS_TYPE_AWG_UPLINK,
    Fields: AwgkernelExitFields,
    /* The type is the whole point: dialled as a wg-client this row would connect
       with no obfuscation at all while claiming to have it. */
    toPayload: (form, remark) => uplinkPayload(form, remark, EGRESS_TYPE_AWG_UPLINK),
    fromRecord: uplinkFromRecord,
  },
};

/* The module for a core kind, or undefined when this build cannot author one. */
export function exitModuleForKind(kind: string): ExitKindModule | undefined {
  return MODULES[kind];
}

/* The module for a stored row, found by the type its driver claims. */
export function exitModuleForType(egressType: string): ExitKindModule | undefined {
  return Object.values(MODULES).find((module) => module.egressType === egressType);
}

/* Which kinds this build can actually author, for gating the picker. */
export function authorableExitKinds(): string[] {
  return Object.keys(MODULES);
}
