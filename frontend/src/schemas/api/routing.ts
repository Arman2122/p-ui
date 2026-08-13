import { z } from 'zod';

/* Criteria and ingressIds are JSON in a column, so they cross the API as the
   values they are. The generated schema types them as unknown; these narrow
   them to what the editor actually reads and writes. */
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

export type RoutingRuleRecord = z.infer<typeof RoutingRuleSchema>;
export type RoutingSubjectView = z.infer<typeof RoutingSubjectViewSchema>;

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

export const DEST_KINDS = ['outbound', 'balancer', 'direct', 'block'] as const;

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

export function criteriaFromForm(form: Record<string, string | undefined>): string {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(form)) {
    const trimmed = (value ?? '').trim();
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
