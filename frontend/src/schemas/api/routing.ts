import { z } from 'zod';

/* Criteria and ingressIds are JSON in a column, so they cross the API as the
   values they are. The generated schema types them as unknown; these narrow
   them to what the editor actually reads and writes. */
/* One refusal the LAST config generation produced for a (rule, subject) pair.
   messageKey is a translation key rather than a sentence so both locales stay
   in charge; args fills its placeholders. Ledger L8. */
export const RoutingDiagSchema = z.object({
  ruleId: z.number(),
  subjectTag: z.string().optional().default(''),
  pattern: z.string().optional().default(''),
  severity: z.string().optional().default(''),
  messageKey: z.string().optional().default(''),
  args: z.record(z.string(), z.string()).optional().default({}),
});

export const RoutingRuleSchema = z.object({
  id: z.number(),
  sortIndex: z.number(),
  enable: z.boolean(),
  remark: z.string().optional().default(''),
  ingressScope: z.string(),
  ingressIds: z.union([z.array(z.number()), z.string()]),
  destKind: z.string(),
  destTag: z.string().optional().default(''),
  destExitId: z.number().nullable().optional(),
  criteria: z.union([z.record(z.string(), z.unknown()), z.string()]),
  inspect: z.boolean().optional().default(false),
  createdAt: z.number().optional(),
  updatedAt: z.number().optional(),
  /* Absent when the rule is realised, and on a panel that has not generated a
     config since it started. The server omits the key rather than sending an
     empty array, so optional is the whole contract. */
  diags: z.array(RoutingDiagSchema).optional(),
});

export const RoutingRuleListSchema = z.array(RoutingRuleSchema);

/* An inbound that cannot be a subject right now carries routable=false and the
   key naming why. criteriaMask is what the editor gates its fields on. */
export const RoutingSubjectViewSchema = z.object({
  inboundId: z.number(),
  tag: z.string(),
  selector: z.string(),
  routable: z.boolean(),
  blockedKey: z.string().optional().default(''),
  criteriaMask: z.array(z.string()).default([]),
});

export const RoutingSubjectViewListSchema = z.array(RoutingSubjectViewSchema);

/* One uplink a rule may be pointed at. What device or port realises it is the
   compile's business and deliberately absent. */
export const RoutingExitViewSchema = z.object({
  id: z.number(),
  label: z.string(),
});

export const RoutingExitViewListSchema = z.array(RoutingExitViewSchema);

export type RoutingRuleRecord = z.infer<typeof RoutingRuleSchema>;
export type RoutingSubjectView = z.infer<typeof RoutingSubjectViewSchema>;
export type RoutingExitView = z.infer<typeof RoutingExitViewSchema>;

/* An uplink rides the destination field the editor already has, under a prefix
   no Xray tag can collide with, so choosing one is the same gesture as choosing
   an outbound instead of a second control that means almost the same thing. */
export const EXIT_TAG_PREFIX = 'exit:';

export function exitTagFor(id: number): string {
  return `${EXIT_TAG_PREFIX}${id}`;
}

export function exitIdFromTag(tag: string | undefined): number | null {
  if (!tag?.startsWith(EXIT_TAG_PREFIX)) return null;
  const id = Number(tag.slice(EXIT_TAG_PREFIX.length));
  return Number.isInteger(id) && id > 0 ? id : null;
}

/* What the server binds. ingressIds and criteria go back as the JSON text the
   column holds, because model.RoutingRule stores them that way. */
export interface RoutingRulePayload {
  enable: boolean;
  remark: string;
  ingressScope: string;
  ingressIds: string;
  destKind: string;
  destTag: string;
  destExitId: number | null;
  criteria: string;
  inspect: boolean;
}

export const DEST_KINDS = ['outbound', 'balancer', 'exit', 'direct', 'block'] as const;

/* Every criterion the editor can offer. A subject's criteriaMask is the subset
   that can actually match on it, and anything outside the mask is disabled with
   its reason rather than hidden. */
export const CRITERIA_FIELDS = [
  'domain',
  'ip',
  'port',
  'sourcePort',
  'network',
  'source',
  'protocol',
  'user',
  'attrs',
] as const;

export type CriterionField = (typeof CRITERIA_FIELDS)[number];

/* Criteria whose xray shape is a list. The editor takes a comma-separated
   string and splits it, which is how the existing rule form already behaves. */
const LIST_CRITERIA = new Set<string>(['domain', 'ip', 'source', 'protocol', 'user']);

export function criteriaToForm(raw: RoutingRuleRecord['criteria']): Record<string, string> {
  const parsed = typeof raw === 'string' ? safeParse(raw) : raw;
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(parsed ?? {})) {
    out[key] = Array.isArray(value) ? value.join(',') : String(value ?? '');
  }
  return out;
}

export function criteriaFromForm(form: Record<string, unknown>): string {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(form)) {
    /* attrs is an object, not a comma list; splitting it would stringify it
       to "[object Object]". No other caller passes a non-string. */
    if (value !== null && typeof value === 'object') {
      if (Object.keys(value as object).length > 0) out[key] = value;
      continue;
    }
    const trimmed = (typeof value === 'string' ? value : '').trim();
    if (!trimmed) continue;
    out[key] = LIST_CRITERIA.has(key)
      ? trimmed.split(',').map((part) => part.trim()).filter(Boolean)
      : trimmed;
  }
  return JSON.stringify(out);
}

export function ingressIdsToArray(raw: RoutingRuleRecord['ingressIds']): number[] {
  if (Array.isArray(raw)) return raw;
  const parsed = safeParse(raw);
  return Array.isArray(parsed) ? (parsed as number[]) : [];
}

function safeParse(raw: string): Record<string, unknown> | unknown[] | null {
  try {
    return JSON.parse(raw) as Record<string, unknown> | unknown[];
  } catch {
    return null;
  }
}

/* The bridge between the two halves of the list. A rule is the same sentence
   either way -- these convert the shape, never the meaning, so one editor can
   open a stored intent row and a template rule without knowing which it has. */

export interface XrayRuleShape {
  enabled?: boolean;
  type?: string;
  domain?: string[];
  ip?: string[];
  port?: string;
  sourcePort?: string;
  network?: string;
  sourceIP?: string[];
  user?: string[];
  inboundTag?: string[];
  protocol?: string[];
  attrs?: Record<string, string>;
  outboundTag?: string;
  balancerTag?: string;
  [key: string]: unknown;
}

const LIST_KEYS: Record<string, 'domain' | 'ip' | 'sourceIP' | 'user' | 'protocol'> = {
  domain: 'domain', ip: 'ip', source: 'sourceIP', user: 'user', protocol: 'protocol',
};

export function intentToRule(
  record: RoutingRuleRecord,
  tagOf: (inboundId: number) => string | undefined,
): XrayRuleShape {
  const criteria = criteriaToForm(record.criteria);
  /* attrs is an object, so it has to come off the PARSED criteria rather than
     the flattened string form. A malformed stored value is ignored, not
     expanded into a row per character in the editor. */
  const parsedCriteria = typeof record.criteria === 'string'
    ? safeParse(record.criteria)
    : record.criteria;
  /* The remark rides through unchanged: the form does not manage it, the modal
     carries every unmanaged key, and ruleToIntent reads it back. Omitting it
     here is what used to blank the column on every edit and every toggle. */
  const out: XrayRuleShape = { type: 'field', enabled: record.enable, remark: record.remark };
  for (const [key, value] of Object.entries(criteria)) {
    if (!value) continue;
    /* Flattening an object criterion yields "[object Object]"; attrs is taken
       from the parsed criteria below instead. */
    if (key === 'attrs') continue;
    const listKey = LIST_KEYS[key];
    if (listKey) out[listKey] = value.split(',').map((s) => s.trim()).filter(Boolean);
    else out[key] = value;
  }
  const attrs = (parsedCriteria as Record<string, unknown> | null | undefined)?.attrs;
  if (attrs && typeof attrs === 'object' && !Array.isArray(attrs)
    && Object.keys(attrs).length > 0) {
    out.attrs = attrs as Record<string, string>;
  }
  out.inboundTag = record.ingressScope === 'all'
    ? []
    : ingressIdsToArray(record.ingressIds).map((id) => tagOf(id) ?? `#${id}`).filter(Boolean);
  if (record.destKind === 'balancer') out.balancerTag = record.destTag;
  else if (record.destKind === 'exit' && record.destExitId != null) {
    out.outboundTag = exitTagFor(record.destExitId);
  } else out.outboundTag = destTagFor(record);
  return out;
}

function destTagFor(record: RoutingRuleRecord): string {
  if (record.destKind === 'direct') return 'direct';
  if (record.destKind === 'block') return 'blocked';
  return record.destTag ?? '';
}

/* A stored rule already IS the payload. Rebuilding one through the editor's
   xray shape is what let a toggle rewrite scope, criteria and destination. */
export function recordToPayload(record: RoutingRuleRecord): RoutingRulePayload {
  return {
    enable: record.enable,
    remark: record.remark,
    ingressScope: record.ingressScope,
    ingressIds: jsonText(record.ingressIds, '[]'),
    destKind: record.destKind,
    destTag: record.destTag ?? '',
    destExitId: record.destExitId ?? null,
    criteria: jsonText(record.criteria, '{}'),
    inspect: record.inspect,
  };
}

/* Both columns are strings on the wire; the schema types them as unions because
   the editor holds the parsed forms. */
function jsonText(raw: string | unknown[] | Record<string, unknown>, empty: string): string {
  if (typeof raw === 'string') return raw || empty;
  return JSON.stringify(raw);
}

/* Refusing rather than returning a payload, because the scope used to be
   INFERRED from what survived resolution: a rule naming one inbound whose tag
   did not resolve came back naming every inbound instead. */
export type IntentResult =
  | { ok: true; payload: RoutingRulePayload }
  | { ok: false; unresolved: string[] };

export function ruleToIntent(
  rule: XrayRuleShape,
  idOf: (tag: string) => number | undefined,
): IntentResult {
  const criteria: Record<string, unknown> = {};
  if (rule.attrs && typeof rule.attrs === 'object' && !Array.isArray(rule.attrs)
    && Object.keys(rule.attrs).length > 0) {
    criteria.attrs = rule.attrs;
  }
  for (const [formKey, ruleKey] of Object.entries(LIST_KEYS)) {
    const value = rule[ruleKey];
    if (Array.isArray(value) && value.length > 0) criteria[formKey] = value.join(',');
  }
  for (const scalar of ['port', 'sourcePort', 'network']) {
    const value = rule[scalar];
    if (typeof value === 'string' && value !== '') criteria[scalar] = value;
  }
  const tags = rule.inboundTag ?? [];
  const ids = tags.map((tag) => idOf(tag)).filter((id): id is number => id != null);
  /* Partial resolution refuses too: silently NARROWING a rule to the inbounds
     that happened to resolve is the same defect pointing the other way. */
  if (ids.length !== tags.length) {
    return { ok: false, unresolved: tags.filter((tag) => idOf(tag) == null) };
  }
  return {
    ok: true,
    payload: {
      enable: rule.enabled !== false,
      remark: typeof rule.remark === 'string' ? rule.remark : '',
      ingressScope: ids.length === 0 ? 'all' : 'selected',
      ingressIds: JSON.stringify(ids),
      ...destinationOf(rule),
      criteria: criteriaFromForm(criteria),
      inspect: false,
    },
  };
}

/* An uplink is stored as its id, never as a tag: the tag in the picker is a
   transport for the choice, and a rule that kept it would name something no
   Xray config contains. */
function destinationOf(rule: XrayRuleShape): Pick<RoutingRulePayload, 'destKind' | 'destTag' | 'destExitId'> {
  const exitId = exitIdFromTag(rule.outboundTag);
  if (exitId != null) return { destKind: 'exit', destTag: '', destExitId: exitId };
  if (rule.balancerTag) return { destKind: 'balancer', destTag: rule.balancerTag, destExitId: null };
  return { destKind: 'outbound', destTag: rule.outboundTag || '', destExitId: null };
}

/* Which criteria can match on every one of these subjects. The INTERSECTION,
   because one rule carries one set and the narrowest subject decides. */
export function intersectMask(subjects: RoutingSubjectView[]): Set<string> {
  if (subjects.length === 0) return new Set(CRITERIA_FIELDS);
  return subjects.slice(1).reduce<Set<string>>(
    (acc, subject) => new Set([...acc].filter((field) => subject.criteriaMask.includes(field))),
    new Set(subjects[0].criteriaMask),
  );
}
