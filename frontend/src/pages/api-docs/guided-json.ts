import { schemaObject } from './request-inputs';
import { formatApiJson } from './request-json';

export interface GuidedJsonField {
  name: string;
  type: 'string' | 'integer' | 'number' | 'boolean';
  required: boolean;
  nullable: boolean;
  description: string;
  format?: string;
  choices?: unknown[];
}
export interface GuidedJsonMember { name: string; rawValue: string; rawEntry: string }

export function guidedJsonFields(schema: unknown): GuidedJsonField[] | undefined {
  const root = schemaObject(schema), properties = schemaObject(root.properties);
  const entries = Object.entries(properties);
  if (root.type !== 'object' || !entries.length || root.allOf || root.anyOf || root.oneOf || root.$ref) return undefined;
  const fields: GuidedJsonField[] = [];
  for (const [name, value] of entries) {
    const property = schemaObject(value);
    const types = Array.isArray(property.type) ? property.type : [property.type];
    const nonnull = types.filter((type) => type !== 'null');
    if (nonnull.length !== 1 || !['string', 'integer', 'number', 'boolean'].includes(String(nonnull[0])) || property.$ref || property.allOf || property.anyOf || property.oneOf) return undefined;
    if (Array.isArray(property.enum) && property.enum.some((item) => item !== null && typeof item === 'object')) return undefined;
    fields.push({ name, type: nonnull[0] as GuidedJsonField['type'], required: Array.isArray(root.required) && root.required.includes(name),
      nullable: property.nullable === true || types.includes('null'), description: typeof property.description === 'string' ? property.description : '',
      format: typeof property.format === 'string' ? property.format : undefined, choices: Array.isArray(property.enum) ? property.enum : undefined });
  }
  if (Array.isArray(root.required) && root.required.some((name) => !Object.hasOwn(properties, String(name)))) return undefined;
  return fields;
}

export function readGuidedJson(raw: string): GuidedJsonMember[] | undefined {
  if (!raw.trim()) return [];
  try { const value: unknown = JSON.parse(raw); if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined; } catch { return undefined; }
  const members: GuidedJsonMember[] = [], names = new Set<string>();
  let at = raw.indexOf('{') + 1;
  const skipWhitespace = () => { while (/\s/.test(raw[at] ?? '') && at < raw.length) at += 1; };
  while (at < raw.length) {
    skipWhitespace();
    if (raw[at] === '}') return members;
    const start = at;
    let escaped = false;
    at += 1;
    while (at < raw.length) {
      const char = raw[at++];
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === '"') break;
    }
    const name = JSON.parse(raw.slice(start, at)) as string;
    if (names.has(name)) return undefined;
    names.add(name);
    skipWhitespace(); at += 1; skipWhitespace();
    const valueStart = at;
    let depth = 0, quoted = false;
    escaped = false;
    while (at < raw.length) {
      const char = raw[at];
      if (quoted) {
        if (escaped) escaped = false;
        else if (char === '\\') escaped = true;
        else if (char === '"') quoted = false;
      } else if (char === '"') quoted = true;
      else if (char === '[' || char === '{') depth += 1;
      else if (char === ']' || char === '}') { if (!depth) break; depth -= 1; }
      else if (char === ',' && !depth) break;
      at += 1;
    }
    members.push({ name, rawValue: raw.slice(valueStart, at).trim(), rawEntry: raw.slice(start, at).trim() });
    if (raw[at] === ',') at += 1;
  }
  return undefined;
}

export function setGuidedJsonValue(raw: string, name: string, rawValue: string | undefined): string | undefined {
  const members = readGuidedJson(raw);
  if (!members) return undefined;
  if (rawValue !== undefined) { try { JSON.parse(rawValue); } catch { return undefined; } }
  let replaced = false;
  const entries = members.flatMap((member) => {
    if (member.name !== name) return [member.rawEntry];
    replaced = true;
    return rawValue === undefined ? [] : [`${JSON.stringify(name)}:${rawValue}`];
  });
  if (!replaced && rawValue !== undefined) entries.push(`${JSON.stringify(name)}:${rawValue}`);
  return formatApiJson(`{${entries.join(',')}}`);
}

export const guidedBufferKey = (name: string) => `guided:${name}`;
export function guidedNumberValid(raw: string): boolean {
  return /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(raw) && Number.isFinite(Number(raw));
}
