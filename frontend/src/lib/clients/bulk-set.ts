/*
The bulk "Set…" form is tri-state per row: keep the field as it is, set it to
a value, or clear it. On the wire an absent field means keep and a present
zero value means clear (0 = unlimited quota or expiry, "" = no flow or group,
policyId 0 = no speed plan), which is what buildBulkSetPatch produces.
*/
export type TriMode = 'keep' | 'set' | 'clear';

export interface TriField<T> {
  mode: TriMode;
  value: T;
}

export interface BulkSetFormValues {
  limitIp: TriField<number>;
  comment: TriField<string>;
  tgId: TriField<number>;
  reset: TriField<number>;
  totalGB: TriField<number>;
  expiryTime: TriField<number | null>;
  flow: TriField<string>;
  group: TriField<string>;
  policyId: TriField<number | null>;
}

export interface BulkSetPatch {
  limitIp?: number;
  comment?: string;
  tgId?: number;
  reset?: number;
  totalGB?: number;
  expiryTime?: number;
  flow?: string;
  group?: string;
  policyId?: number;
}

export const GB = 1024 * 1024 * 1024;

export const EMPTY_BULK_SET: BulkSetFormValues = {
  limitIp: { mode: 'keep', value: 0 },
  comment: { mode: 'keep', value: '' },
  tgId: { mode: 'keep', value: 0 },
  reset: { mode: 'keep', value: 0 },
  totalGB: { mode: 'keep', value: 0 },
  expiryTime: { mode: 'keep', value: null },
  flow: { mode: 'keep', value: '' },
  group: { mode: 'keep', value: '' },
  policyId: { mode: 'keep', value: null },
};

function numberField(field: TriField<number | null>, scale = 1): number | undefined {
  if (field.mode === 'keep') return undefined;
  if (field.mode === 'clear') return 0;
  return Math.max(0, Math.trunc((field.value ?? 0) * scale));
}

function stringField(field: TriField<string>): string | undefined {
  if (field.mode === 'keep') return undefined;
  if (field.mode === 'clear') return '';
  return field.value.trim();
}

export function buildBulkSetPatch(values: BulkSetFormValues): BulkSetPatch {
  const patch: BulkSetPatch = {};
  const limitIp = numberField(values.limitIp);
  if (limitIp !== undefined) patch.limitIp = limitIp;
  const comment = stringField(values.comment);
  if (comment !== undefined) patch.comment = comment;
  const tgId = numberField(values.tgId);
  if (tgId !== undefined) patch.tgId = tgId;
  const reset = numberField(values.reset);
  if (reset !== undefined) patch.reset = reset;
  const totalGB = numberField(values.totalGB, GB);
  if (totalGB !== undefined) patch.totalGB = totalGB;
  const expiryTime = numberField(values.expiryTime);
  if (expiryTime !== undefined) patch.expiryTime = expiryTime;
  const flow = stringField(values.flow);
  if (flow !== undefined) patch.flow = flow;
  const group = stringField(values.group);
  if (group !== undefined) patch.group = group;
  const policyId = numberField(values.policyId);
  if (policyId !== undefined) patch.policyId = policyId;
  return patch;
}

export function bulkSetPatchIsEmpty(patch: BulkSetPatch): boolean {
  return Object.keys(patch).length === 0;
}

/* A row switched to Set but left without a value would silently act as Clear. */
export function firstBulkSetIssue(values: BulkSetFormValues): string | null {
  const missing =
    (values.expiryTime.mode === 'set' && !values.expiryTime.value) ||
    (values.policyId.mode === 'set' && !values.policyId.value) ||
    (values.flow.mode === 'set' && values.flow.value.trim() === '') ||
    (values.group.mode === 'set' && values.group.value.trim() === '') ||
    (values.totalGB.mode === 'set' && !(values.totalGB.value > 0)) ||
    (values.tgId.mode === 'set' && !(values.tgId.value > 0));
  if (missing) return 'pages.clients.bulkSetMissingValue';
  if (bulkSetPatchIsEmpty(buildBulkSetPatch(values))) return 'pages.clients.bulkSetNothing';
  return null;
}
