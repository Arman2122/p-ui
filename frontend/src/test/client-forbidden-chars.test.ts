import { describe, it, expect } from 'vitest';

import { hasForbiddenClientChars } from '@/schemas/client';

/*
The form must refuse the whitespace the backend refuses.

The Go side rejects an email or subId containing any unicode.IsSpace rune. The
form checked only the ASCII space, so a value pasted from a document or a chat
app -- which routinely carries U+00A0 -- passed validation, went to the server,
and came back as a raw backend error with nothing marking the field.

U+0085 and U+00A0 are called out because JavaScript's \s does not include them,
which is exactly the gap that let this through. F176.
*/
describe('hasForbiddenClientChars', () => {
  const rejected: Record<string, string> = {
    'ASCII space': 'a b',
    'non-breaking space U+00A0': 'a\u00a0b',
    'next line U+0085': 'a\u0085b',
    'en quad U+2000': 'a\u2000b',
    'line separator U+2028': 'a\u2028b',
    'narrow no-break U+202F': 'a\u202fb',
    'ideographic space U+3000': 'a\u3000b',
    'tab': 'a\tb',
    'forward slash': 'a/b',
    'backslash': 'a\b',
    'control character': 'a\u0001b',
  };
  for (const [name, value] of Object.entries(rejected)) {
    it(`rejects ${name}`, () => {
      expect(hasForbiddenClientChars(value), `${name} reaches the backend and comes back as a raw error`).toBe(true);
    });
  }

  const accepted = ['alice@example.com', 'sub-1234', 'user+tag@example.com', 'a&b', 'کاربر'];
  for (const value of accepted) {
    it(`accepts ${JSON.stringify(value)}`, () => {
      expect(hasForbiddenClientChars(value), 'a legal value was refused').toBe(false);
    });
  }
});
