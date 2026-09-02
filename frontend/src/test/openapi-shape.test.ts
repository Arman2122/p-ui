import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/*
The published openapi.json must be valid OpenAPI 3.0.3.

build-openapi.mjs folded every entry of endpoints.ts into the Paths Object,
including the four WebSocket message shapes documented as pseudo-endpoints with
a `WS` method and a `→ type: …` path. A Paths key must begin with `/` and a
Path Item may carry only HTTP methods, so every tool that validates the
document rejected it before reading a single real endpoint. F408.

The shapes are not lost: they live in `x-websocket-messages`, which the docs
site renders onto the WebSocket page.
*/
describe('openapi.json', () => {
  const spec = JSON.parse(readFileSync(resolve(process.cwd(), 'public', 'openapi.json'), 'utf8')) as {
    paths: Record<string, Record<string, unknown>>;
    'x-websocket-messages'?: { type: string; summary: string }[];
  };
  const httpMethods = new Set(['get', 'post', 'put', 'delete', 'patch', 'head', 'options', 'trace']);

  it('has only real paths and HTTP methods in the Paths Object', () => {
    const badPaths = Object.keys(spec.paths).filter((p) => !p.startsWith('/'));
    expect(badPaths, 'Paths Object keys that are not paths').toEqual([]);

    const badMethods = Object.entries(spec.paths)
      .flatMap(([p, item]) => Object.keys(item).filter((m) => !httpMethods.has(m)).map((m) => `${m} ${p}`));
    expect(badMethods, 'Path Item keys that are not HTTP methods').toEqual([]);
  });

  it('keeps the WebSocket message shapes in the vendor extension', () => {
    const types = (spec['x-websocket-messages'] ?? []).map((m) => m.type).sort();
    expect(types).toEqual(['invalidate', 'notification', 'status', 'xray_state']);
  });
});
