import { describe, expect, it } from 'vitest';
import { guidedJsonFields, readGuidedJson, setGuidedJsonValue } from '@/pages/api-docs/guided-json';

const schema = { type: 'object', required: ['username'], properties: { username: { type: 'string' }, password: { type: 'string', format: 'password' }, enabled: { type: 'boolean' }, count: { type: 'integer' }, optional: { type: ['string', 'null'] } } };

describe('lossless guided request bodies', () => {
  it('offers primitive fields but leaves nested, composite and unspecified schemas in JSON', () => {
    expect(guidedJsonFields(schema)?.map((field) => [field.name, field.type, field.required, field.nullable])).toEqual([
      ['username', 'string', true, false], ['password', 'string', false, false], ['enabled', 'boolean', false, false], ['count', 'integer', false, false], ['optional', 'string', false, true],
    ]);
    expect(guidedJsonFields({ type: 'object', properties: { nested: { type: 'object' } } })).toBeUndefined();
    expect(guidedJsonFields({ type: 'array', items: schema })).toBeUndefined();
    expect(guidedJsonFields({ type: 'object', properties: { value: { oneOf: [{ type: 'string' }, { type: 'integer' }] } } })).toBeUndefined();
  });

  it('round-trips omitted, false, zero and null independently', () => {
    let raw = setGuidedJsonValue('', 'enabled', 'false')!;
    raw = setGuidedJsonValue(raw, 'count', '0')!;
    raw = setGuidedJsonValue(raw, 'optional', 'null')!;
    expect(JSON.parse(raw)).toEqual({ enabled: false, count: 0, optional: null });
    raw = setGuidedJsonValue(raw, 'optional', undefined)!;
    expect(JSON.parse(raw)).toEqual({ enabled: false, count: 0 });
    expect(readGuidedJson(raw)?.find((member) => member.name === 'username')).toBeUndefined();
  });

  it('preserves unknown nested properties, huge integers and escapes when one field changes', () => {
    const escaped = JSON.stringify('a " quote');
    const original = `{"username":"old","unknown":{"id":18446744073709551615,"escaped":${escaped}},"fraction":1.230000e+40}`;
    const changed = setGuidedJsonValue(original, 'username', '"new"')!;
    expect(changed).toContain('18446744073709551615');
    expect(changed).toContain('1.230000e+40');
    expect(readGuidedJson(changed)?.find((member) => member.name === 'unknown')?.rawValue).toContain('18446744073709551615');
    expect(JSON.parse(changed).unknown.escaped).toBe('a " quote');
  });

  it('does not repair or replace invalid or ambiguous JSON during mode changes', () => {
    for (const raw of ['{"username":', '{"username":1,"username":2}', '[1,2]', 'null']) {
      expect(readGuidedJson(raw)).toBeUndefined();
      expect(setGuidedJsonValue(raw, 'username', '"new"')).toBeUndefined();
    }
    expect(setGuidedJsonValue('{"count":12}', 'count', '-')).toBeUndefined();
  });
});
