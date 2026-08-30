import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import { BulkAddHostSchema } from '@/schemas/api/host';
import { rhfZodValidate } from '@/components/form/rhf';

/*
The host form declared rules it never ran.

Its useForm carries no resolver, so a field is checked only if it names one
itself -- and exactly three did. port, tags, serverDescription and vlessRoute
were submitted whatever they held, including a vlessRoute the Zod schema
already had a regex and a message for. The panel stores what arrives, and
applyVlessRoute then returns the UUID unchanged, so a malformed route is a link
that quietly routes nowhere.

Every message these produce is a key, because FormField renders
t(message, { defaultValue: message }) -- an unkeyed rule shows the operator
Zod's own English sentence instead. F193.
*/

const shape = BulkAddHostSchema.shape;

describe('the host form fields that were never checked', () => {
  it.each([
    ['port', shape.port, 70000, 'pages.hosts.toasts.badPort'],
    ['tags', shape.tags, ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K'], 'pages.hosts.toasts.tooManyTags'],
    ['tags', shape.tags, ['lower case'], 'pages.hosts.toasts.badTag'],
    ['serverDescription', shape.serverDescription, 'x'.repeat(65), 'pages.hosts.toasts.serverDescriptionTooLong'],
    ['vlessRoute', shape.vlessRoute, 'not-a-port', 'pages.hosts.toasts.badVlessRoute'],
  ])('%s refuses a bad value with a translatable key', (_name, field, bad, key) => {
    expect(rhfZodValidate(field)(bad)).toBe(key);
  });

  it.each([
    ['port', shape.port, 443],
    ['tags', shape.tags, ['CDN', 'EU_WEST']],
    ['serverDescription', shape.serverDescription, 'Frankfurt edge'],
    ['vlessRoute', shape.vlessRoute, '443'],
    ['vlessRoute', shape.vlessRoute, ''],
  ])('%s accepts an ordinary value', (_name, field, good) => {
    expect(rhfZodValidate(field)(good)).toBe(true);
  });
});

// And the form really attaches them — the half a schema test cannot see, and
// the half that was missing.
describe('the host form', () => {
  const source = readFileSync(
    resolve(__dirname, '../pages/hosts/HostFormModal.tsx'),
    'utf8',
  );

  it.each(['serverDescription', 'port', 'tags', 'vlessRoute'])(
    '%s carries the rule that checks it',
    (name) => {
      const at = source.indexOf(`<FormField name="${name}"`);
      expect(at, `the ${name} field is gone; this guard is now vacuous`).toBeGreaterThan(-1);
      const tag = source.slice(at, source.indexOf('>', at));
      expect(tag).toContain(`rhfZodValidate(BulkAddHostSchema.shape.${name})`);
    },
  );
});
