import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

import { describe, expect, it } from 'vitest';

interface RequestSchema {
  $ref?: string;
  type?: string;
  required?: string[];
  nullable?: boolean;
  properties?: Record<string, RequestSchema>;
  items?: RequestSchema;
  enum?: unknown[];
  minimum?: number;
}

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
  const generator = pathToFileURL(resolve(process.cwd(), 'scripts', 'build-openapi.mjs')).href;
  const spec = JSON.parse(execFileSync(process.execPath, ['--input-type=module', '--eval',
    `import { buildSpec } from ${JSON.stringify(generator)}; process.stdout.write(JSON.stringify(buildSpec()));`,
  ], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 })) as {
    paths: Record<string, Record<string, {
      security?: Record<string, string[]>[];
      servers?: { url: string; variables?: Record<string, { default: string }> }[];
      parameters?: { name: string; in: string; required?: boolean }[];
      requestBody?: { required: boolean; content: Record<string, { schema: RequestSchema; example?: Record<string, unknown> }> };
      responses: Record<string, { content?: Record<string, { schema: Record<string, unknown> }> }>;
    }>>;
    'x-websocket-messages'?: { type: string; summary: string }[];
    components: { schemas: Record<string, RequestSchema> };
  };
  const httpMethods = new Set(['get', 'post', 'put', 'delete', 'patch', 'head', 'options', 'trace']);
  function resolveSchema(schema: RequestSchema | undefined): RequestSchema {
    expect(schema).toBeDefined();
    return schema?.$ref ? spec.components.schemas[schema.$ref.split('/').at(-1)!] : schema!;
  }
  function request(path: string, media = 'application/json', required = true): RequestSchema {
    const body = spec.paths[path].post.requestBody;
    expect(body, `${path} request body`).toBeDefined();
    expect(body?.required, `${path} body presence`).toBe(required);
    return resolveSchema(body?.content[media]?.schema);
  }

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

  it('describes binary downloads and the raw OpenAPI document without a Msg wrapper', () => {
    for (const path of ['/panel/api/server/getDb', '/panel/api/backups/{id}/download', '/{subPath}{subid}/file/{inboundId}']) {
      expect(spec.paths[path].get.responses['200'].content?.['application/octet-stream']?.schema, path)
        .toEqual({ type: 'string', format: 'binary' });
    }
    expect(spec.paths['/panel/api/openapi.json'].get.responses['200'].content?.['application/json'].schema)
      .toEqual({ type: 'object', additionalProperties: true });
    expect(spec.paths['/panel/api/me'].get.responses['200'].content?.['application/json'].schema)
      .toMatchObject({ properties: { success: { type: 'boolean' }, obj: { $ref: '#/components/schemas/SignedInUser' } } });
    expect(spec.paths['/panel/api/backups/{id}/download'].get.responses['200'].content?.['application/json'].schema)
      .toMatchObject({ properties: { success: { enum: [false] } } });
  });

  it('describes subscription negotiation on the separate public listener', () => {
    const raw = spec.paths['/{subPath}{subid}'].get;
    expect(raw.security).toEqual([]);
    expect(raw.servers).toMatchObject([{ url: '{subscriptionServer}', variables: { subscriptionServer: { default: 'http://localhost:2096' } } }]);
    expect(Object.keys(raw.responses['200'].content ?? {}).sort()).toEqual(['application/json', 'application/yaml', 'text/html', 'text/plain']);
    expect(raw.responses['200'].content?.['text/plain'].schema).toEqual({ type: 'string' });
    const json = spec.paths['/{jsonPath}{subid}'].get;
    expect(json.security).toEqual([]);
    expect(Object.keys(json.responses['200'].content ?? {}).sort()).toEqual(['application/json', 'text/html', 'text/plain']);
    expect(json.responses['200'].content?.['application/json'].schema)
      .toEqual({ oneOf: [{ type: 'object', additionalProperties: true }, { type: 'array', items: { type: 'object', additionalProperties: true } }] });
    const clash = spec.paths['/{clashPath}{subid}'].get;
    expect(clash.security).toEqual([]);
    expect(Object.keys(clash.responses['200'].content ?? {}).sort()).toEqual(['application/yaml', 'text/html']);
    expect(spec.paths['/{subPath}{subid}/file/{inboundId}'].get.security).toEqual([]);
  });

  it('models WebSocket as an administrator-cookie upgrade, not an HTTP JSON response', () => {
    const operation = spec.paths['/ws'].get;
    expect(operation.security).toEqual([{ cookieAuth: [] }]);
    expect(operation.responses['101']).toBeDefined();
    expect(operation.responses['101'].content).toBeUndefined();
    expect(operation.responses['200']).toBeUndefined();
  });

  it('keeps public bootstrap and cookie CSRF requirements distinct from API bearer auth', () => {
    expect(spec.paths['/csrf-token'].get.security).toEqual([]);
    for (const path of ['/login', '/getTwoFactorEnable']) {
      expect(spec.paths[path].post.security).toEqual([]);
      expect(spec.paths[path].post.parameters).toContainEqual(expect.objectContaining({ name: 'X-CSRF-Token', in: 'header', required: true }));
    }
    expect(spec.paths['/logout'].post.security).toEqual([{ cookieAuth: [] }]);
    expect(spec.paths['/panel/api/clients/groups/create'].post.parameters)
      .toContainEqual(expect.objectContaining({ name: 'X-CSRF-Token', in: 'header', required: false }));
    expect(spec.paths['/panel/api/clients/groups/create'].post.security).toBeUndefined();
  });

  it('can construct routing writes with JSON encoded string fields and an inbound update', () => {
    const model = readFileSync(resolve(process.cwd(), '../internal/database/model/model.go'), 'utf8');
    expect(model).toMatch(/IngressIds\s+string\s+`json:"ingressIds"/);
    expect(model).toMatch(/Criteria\s+string\s+`json:"criteria"/);
    for (const path of ['/panel/api/routing/rules', '/panel/api/routing/rules/{id}']) {
      expect(request(path).properties).toMatchObject({ ingressIds: { type: 'string' }, criteria: { type: 'string' }, destKind: { type: 'string' } });
      const example = spec.paths[path].post.requestBody?.content['application/json'].example;
      expect(typeof example?.ingressIds).toBe('string');
      expect(typeof example?.criteria).toBe('string');
      expect(JSON.parse(String(example?.ingressIds))).toEqual([3]);
    }
    expect(request('/panel/api/routing/rules/order').properties).toMatchObject({ ids: { type: 'array', items: { type: 'integer' } } });
    expect(request('/panel/api/inbounds/update/{id}').properties).toHaveProperty('settings');
  });

  it('exposes the form fields the log and outbound subscription handlers actually read', () => {
    for (const [path, fields] of [
      ['/panel/api/server/logs/{count}', ['level', 'syslog']],
      ['/panel/api/xray/outbound-subs/{id}', ['remark', 'url', 'tagPrefix', 'enabled', 'allowPrivate', 'allowInsecure', 'prepend', 'updateInterval']],
    ] as const) {
      const schema = request(path, 'application/x-www-form-urlencoded', !path.includes('/logs/'));
      for (const name of fields) expect(schema.properties, `${path}: ${name}`).toHaveProperty(name);
      expect(spec.paths[path].post.requestBody?.content['application/json']).toBeUndefined();
    }
  });

  it('keeps body presence separate from optional fields and preserves endpoint defaults', () => {
    expect(request('/login').required).toEqual(['username', 'password']);
    expect(request('/panel/api/policies/reapply/{id}').required ?? []).toEqual([]);
    const token = request('/panel/api/setting/apiTokens/create');
    expect(token.required).toContain('name');
    expect(token.required).not.toContain('scope');
    expect(token.required).not.toContain('expiresAt');
    expect(spec.paths['/panel/api/clients/del/{email}'].post.parameters)
      .toContainEqual(expect.objectContaining({ name: 'keepTraffic', in: 'query', required: false }));
    expect(request('/panel/api/policies/bans/add').required).not.toContain('minutes');
    expect(request('/panel/api/policies/bans/release').required ?? []).not.toContain('email');
  });

  it('documents login credential failure at HTTP 200 alongside its success result', () => {
    const responses = spec.paths['/login'].post.responses;
    expect(responses['400']).toBeUndefined();
    expect(responses['200']).toMatchObject({ content: { 'application/json': { examples: {
      success: { value: { success: true } }, failure: { value: { success: false } },
    } } } });
  });

  it('preserves the nested client create contract, bulk array, and flat update options', () => {
    const source = readFileSync(resolve(process.cwd(), '../internal/web/service/client.go'), 'utf8');
    expect(source).toMatch(/Client\s+model\.Client\s+`json:"client"/);
    for (const path of ['/panel/api/clients/add', '/panel/api/clients/bulkCreate']) {
      const schema = request(path);
      if (path.endsWith('bulkCreate')) expect(schema.type).toBe('array');
      const payload = path.endsWith('bulkCreate') ? resolveSchema(schema.items) : schema;
      expect(payload.properties).toHaveProperty('client');
      expect(payload.properties).not.toHaveProperty('email');
      expect(payload.properties).toHaveProperty('externalLinks');
      expect(resolveSchema(payload.properties?.client).required).toEqual(['email']);
      expect(resolveSchema(payload.properties?.client).properties?.totalGB).toMatchObject({ description: expect.stringContaining('bytes') });
    }
    expect(spec.components.schemas.Client.required).toContain('subId');
    const update = request('/panel/api/clients/update/{email}');
    expect(update.properties).toMatchObject({ email: { type: 'string' }, totalGB: { type: 'integer' }, inboundIds: { type: 'array', items: { type: 'integer' } } });
    expect(update.properties).toHaveProperty('planOverrides');
    expect(spec.paths['/panel/api/clients/update/{email}'].post.parameters)
      .toContainEqual(expect.objectContaining({ name: 'inboundIds', in: 'query', required: false }));
  });

  it('describes complete policy and node request shapes without response-only required fields', () => {
    for (const path of ['/panel/api/policies/add', '/panel/api/policies/update/{id}', '/panel/api/reseller/plans/add', '/panel/api/reseller/plans/update/{id}']) {
      const schema = request(path);
      expect(schema.properties).toMatchObject({ name: { type: 'string' }, burst: { type: 'boolean' }, tiers: { type: 'array' }, quotaBytes: { nullable: true }, durationDays: { nullable: true }, limitIp: { nullable: true } });
      expect(schema.required).not.toContain('assignedClients');
    }
    for (const path of ['/panel/api/nodes/add', '/panel/api/nodes/update/{id}', '/panel/api/nodes/test', '/panel/api/nodes/certFingerprint', '/panel/api/nodes/inbounds']) {
      const schema = request(path);
      expect(schema.required).toEqual(['name', 'address', 'port']);
      expect(schema.properties).toMatchObject({ inboundTags: { type: 'array', items: { type: 'string' } }, tlsVerifyMode: { enum: ['verify', 'skip', 'pin', 'mtls'] } });
      expect(spec.paths[path].post.requestBody?.content['application/json'].example?.name).toBeTruthy();
    }
  });

  it('accepts partial reseller top-ups while requiring an explicit non-null suspension boolean', () => {
    const suspend = request('/panel/api/resellers/{id}/suspend');
    expect(suspend.required).toEqual(['suspended']);
    expect(suspend.properties?.suspended).toMatchObject({ type: 'boolean', nullable: false });
    expect(request('/panel/api/resellers/{id}/top-up').required ?? []).toEqual([]);
    const update = request('/panel/api/resellers/{id}/update');
    expect(update.required).toEqual(['name', 'username', 'inboundIds']);
    for (const name of ['password', 'email', 'note']) expect(update.properties).toHaveProperty(name);
  });

  it('retains bulk filters and operation fields without requiring explicit email selection', () => {
    const fields: Record<string, string[]> = {
      bulkAdjust: ['addDays', 'addBytes', 'flow', 'mode'],
      bulkSet: ['limitIp', 'comment', 'tgId', 'reset', 'totalGB', 'expiryTime', 'flow', 'group', 'policyId'],
      bulkMove: ['fromInboundIds', 'toInboundIds'], bulkRotate: ['credentials', 'subId'], bulkDel: ['keepTraffic'],
      bulkAttach: ['inboundIds'], bulkDetach: ['inboundIds'], 'groups/bulkAdd': ['group'], 'groups/bulkRemove': [],
      bulkClearIps: [], bulkExport: [], bulkLinks: [], bulkEnable: [], bulkDisable: [], bulkResetTraffic: [],
    };
    for (const [suffix, names] of Object.entries(fields)) {
      const schema = request('/panel/api/clients/' + suffix);
      expect(schema.properties).toMatchObject({ emails: { type: 'array', items: { type: 'string' } }, filter: { type: 'object' } });
      expect(schema.required ?? []).not.toContain('emails');
      for (const name of names) expect(schema.properties, suffix).toHaveProperty(name);
    }
    expect(request('/panel/api/clients/bulkAdjust').properties?.mode.enum).toEqual(['add', 'set']);
    expect(request('/panel/api/clients/bulkAdjust').properties?.addDays.minimum).toBeUndefined();
    const patch = request('/panel/api/clients/bulkSet');
    for (const name of ['limitIp', 'tgId', 'reset', 'totalGB', 'expiryTime', 'policyId']) {
      expect(patch.properties?.[name], name).toMatchObject({ type: 'integer', nullable: true, minimum: 0 });
    }
    for (const name of ['comment', 'flow', 'group']) {
      expect(patch.properties?.[name], name).toMatchObject({ type: 'string', nullable: true });
    }
  });
});
