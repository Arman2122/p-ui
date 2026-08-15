import type { ComponentType } from 'react';

import {
  EGRESS_TYPE_UPLINK,
  EgressFormFields,
  UplinkSettingsSchema,
  uplinkPayload,
  type EgressFormValues,
  type EgressPayload,
  type EgressRecord,
} from '@/schemas/api/egress';

import WgkernelExitFields from './wgkernel-fields';

/*
One module per kind a route can exit on.

The two vocabularies meet here and nowhere else: a CORE KIND is what the registry
declares and what the picker shows, an EGRESS TYPE is what the row is stored as
and which driver dials it. Before this, both were the WireGuard uplink's — so a
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

function wgkernelFromRecord(row: EgressRecord): EgressFormValues {
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
    };
  } catch {
    return base;
  }
}

const MODULES: Record<string, ExitKindModule> = {
  wgkernel: {
    egressType: EGRESS_TYPE_UPLINK,
    Fields: WgkernelExitFields,
    toPayload: uplinkPayload,
    fromRecord: wgkernelFromRecord,
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
