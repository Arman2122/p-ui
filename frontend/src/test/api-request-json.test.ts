import { describe, expect, it } from 'vitest';
import { formatApiJson } from '@/pages/api-docs/request-json';

describe('lossless API response formatting', () => {
  it('indents objects and arrays without converting large integers or exponent literals', () => {
    const raw = '{"up":18446744073709551615,"ratio":1.2300e+30,"items":[-9007199254740993,0],"empty":{}}';
    expect(formatApiJson(raw)).toBe('{\n  "up": 18446744073709551615,\n  "ratio": 1.2300e+30,\n  "items": [\n    -9007199254740993,\n    0\n  ],\n  "empty": {}\n}');
  });

  it('preserves escaped quotes, backslashes and whitespace within JSON strings', () => {
    const value = 'keep  spaces, : { } " quote \\ slash';
    const raw = JSON.stringify({ text: value, nested: [{}, []] });
    const formatted = formatApiJson(raw);
    expect(formatted).toContain(JSON.stringify(value));
    expect(JSON.parse(formatted)).toEqual(JSON.parse(raw));
    expect(formatted).toContain('[\n    {},\n    []\n  ]');
  });
});
