import {
  CAPABILITY_RULES,
  type CapabilityClause,
  type CapabilityRule,
} from '@/generated/capabilities';

/*
The TypeScript twin of internal/core/capability.go.

The rules themselves are generated from Go, so only this evaluator exists twice,
and src/test/capabilities.test.ts replays a Go-generated truth table through it —
change a clause in Go, leave this alone, and that test goes red. That is what
stops "may this inbound do X" acquiring a fourth hand-written implementation.
*/

export interface CapabilityFacts {
  protocol: string;
  stream?: Record<string, string | undefined>;
  settings?: Record<string, string | undefined>;
}

function lookup(facts: CapabilityFacts, field: string): string {
  if (field === 'protocol') return facts.protocol ?? '';
  if (field.startsWith('stream.')) return facts.stream?.[field.slice('stream.'.length)] ?? '';
  if (field.startsWith('settings.')) return facts.settings?.[field.slice('settings.'.length)] ?? '';
  return '';
}

function evaluateClause(clause: CapabilityClause, facts: CapabilityFacts): boolean {
  const value = lookup(facts, clause.field);
  let got: boolean;
  switch (clause.op) {
    case 'in':
      got = (clause.values ?? []).includes(value);
      break;
    case 'set':
      /* "none" is xray's explicit off-switch and reads as unset everywhere. */
      got = value !== '' && value !== 'none';
      break;
    case 'prefix':
      got = (clause.values ?? []).some((v) => v !== '' && value.startsWith(v));
      break;
    default:
      return false;
  }
  return clause.not ? !got : got;
}

export function evaluateRule(rule: CapabilityRule, facts: CapabilityFacts): boolean {
  return (rule.any ?? []).some(
    (term) => (term.all?.length ?? 0) > 0 && term.all.every((c) => evaluateClause(c, facts)),
  );
}

/* An unknown capability name is false, never true. */
export function can(name: string, facts: CapabilityFacts): boolean {
  const rule = CAPABILITY_RULES[name];
  return rule ? evaluateRule(rule, facts) : false;
}

export function resolveAll(facts: CapabilityFacts): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const [name, rule] of Object.entries(CAPABILITY_RULES)) {
    out[name] = evaluateRule(rule, facts);
  }
  return out;
}
