import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { sections, type Endpoint } from '@/pages/api-docs/endpoints';
import {
  ApiDocumentSchema, ApiOperationDetailsSchema, buildApiCatalogue, formatApiExample, getOperation, getOperationDetails, getParameters,
  getRequest, getResponses, inlineDocText, normalizeApiPath, resolveLocalRef, schemaFields,
} from '@/pages/api-docs/api-document';

const served = ApiDocumentSchema.parse(JSON.parse(readFileSync(new URL('../../public/openapi.json', import.meta.url), 'utf8')));
const document = (extra: Record<string, unknown> = {}) => ApiDocumentSchema.parse({ openapi: '3.0.3', info: { title: 'test', version: '1' }, paths: {}, ...extra });

describe('served API document and endpoint catalogue', () => {
  it('validates the actual generated document and includes HTTP operations and distinct WS messages', () => {
    const catalogue = buildApiCatalogue(served);
    const http = catalogue.filter((entry) => entry.kind === 'http');
    const operationCount = Object.values(served.paths).reduce((count, item) => count + ['get', 'post', 'put', 'patch', 'delete'].filter((method) => item[method]).length, 0);
    expect(http.filter((entry) => entry.available)).toHaveLength(operationCount);
    expect(http.filter((entry) => !entry.available)).toEqual([]);
    const messages = catalogue.filter((entry) => entry.kind === 'websocket');
    expect(messages.map((entry) => entry.messageType)).toEqual(['status', 'xray_state', 'notification', 'invalidate']);
    for (const entry of messages) {
      const compatible: Endpoint = entry;
      expect(getOperation(served, compatible)).toBeUndefined();
      expect(entry.available).toBe(true);
      expect(getOperationDetails(served, entry)?.examples[0].value).toMatchObject({ type: entry.messageType });
      expect(getOperationDetails(served, entry)?.responses).toEqual([]);
    }
    expect(ApiDocumentSchema.safeParse({ paths: {} }).success).toBe(false);
    expect(ApiDocumentSchema.safeParse({ ...served, paths: [] }).success).toBe(false);
  });

  it('merges new served operations without confusing verbs, registry omissions, or websocket paths', () => {
    const doc = document({ paths: { '/items/{id}': { get: { summary: 'Read', responses: {} }, post: { summary: 'Write', responses: {} } }, '/extra': { delete: { tags: ['Extras'], responses: {} } } } });
    const registry = [{ id: 'items', title: 'Items', endpoints: [
      { method: 'GET' as const, path: '/items/:id', summary: 'old read' },
      { method: 'PUT' as const, path: '/items/:id', summary: 'unserved' },
    ] }];
    const entries = buildApiCatalogue(doc, registry);
    expect(entries.map((entry) => [entry.key, entry.available])).toEqual([
      ['GET /items/{id}', true], ['PUT /items/{id}', false], ['POST /items/{id}', true], ['DELETE /extra', true],
    ]);
    expect(entries[0].summary).toBe('Read');
    expect(getOperationDetails(doc, entries[1])).toBeUndefined();
    expect(getOperation(doc, { method: 'POST', path: '/items/:id' })?.summary).toBe('Write');
    expect(getOperation(doc, { method: 'GET', path: '/items/missing' })).toBeUndefined();
  });

  it('keeps configurable subscription prefixes and registry WS examples without inventing HTTP operations', () => {
    expect(normalizeApiPath('/{subPath}:subid/file/:inboundId')).toBe('/{subPath}{subid}/file/{inboundId}');
    const registry = sections.filter((section) => section.id === 'websocket');
    const entries = buildApiCatalogue(document(), registry);
    expect(entries.find((entry) => entry.path === '/ws')?.available).toBe(false);
    expect(entries.filter((entry) => entry.kind === 'websocket').every((entry) => entry.available && entry.examples.length === 1)).toBe(true);
    const sub = buildApiCatalogue(served).find((entry) => entry.path === '/{subPath}:subid/file/:inboundId')!;
    expect(sub.groupId).toBe('subscription');
    expect(sub.subHeader?.some((header) => header.name === 'Subscription-Userinfo')).toBe(true);
    expect(getParameters(served, sub).filter((parameter) => parameter.in === 'path').map((parameter) => parameter.name)).toEqual(expect.arrayContaining(['subPath', 'subid', 'inboundId']));
  });

  it('produces bounded, typed static details for every generated operation', () => {
    for (const entry of buildApiCatalogue(served)) {
      const detail = getOperationDetails(served, entry);
      expect(detail, entry.key).toBeDefined();
      expect(ApiOperationDetailsSchema.safeParse(detail).success, entry.key).toBe(true);
      expect(detail?.issues, entry.key).toEqual([]);
      for (const medium of [...(detail?.requestBody?.content ?? []), ...(detail?.responses.flatMap((response) => response.content) ?? [])]) {
        expect(medium.fields.length).toBeLessThanOrEqual(300);
      }
    }
  });
});

describe('generated operation metadata', () => {
  it('merges inherited parameters with operation overrides and preserves serialization metadata', () => {
    const doc = document({ components: { parameters: { Id: { name: 'id', in: 'path', schema: { type: 'integer' } } } }, paths: {
      '/items/{id}': { parameters: [{ $ref: '#/components/parameters/Id' }, { name: 'q', in: 'query', schema: { type: 'string' } }], get: { parameters: [
        { name: 'q', in: 'query', required: true, style: 'form', explode: false, allowReserved: true, schema: { type: 'array', items: { type: 'integer' }, default: [0] }, example: [0, 2] },
        { name: 'q', in: 'header', schema: { type: 'string' }, example: '' },
      ] } },
    } });
    const parameters = getParameters(doc, { method: 'GET', path: '/items/:id' });
    expect(parameters).toHaveLength(3);
    expect(parameters[0]).toMatchObject({ name: 'id', in: 'path', required: true, type: 'integer' });
    expect(parameters[1]).toMatchObject({ name: 'q', in: 'query', required: true, style: 'form', explode: false, allowReserved: true, examples: [{ value: [0, 2] }] });
    expect(parameters[2].examples[0].value).toBe('');
  });

  it('reads real generated response schemas and examples without substituting registry payloads', () => {
    const endpoint = sections.flatMap((section) => section.endpoints).find((entry) => entry.responseSchema === 'InboundOption')!;
    const op = getOperation(served, endpoint)!;
    const responses = getResponses(served, op);
    const raw = served.paths[normalizeApiPath(endpoint.path)].get?.responses?.['200'];
    const expected = (raw as { content: { 'application/json': { example: unknown } } }).content['application/json'].example;
    expect(responses[0].content[0].examples[0].value).toEqual(expected);
    expect(responses[0].content[0].fields.some((field) => field.path.endsWith('.tlsFlowCapable'))).toBe(true);
    expect(getResponses(document(), { responses: { '204': { description: 'No content' } } })).toEqual([{ status: '204', description: 'No content', headers: [], content: [] }]);
  });

  it('supports JSON, form, multipart files and per-property encoding from the spec', () => {
    const op = getOperation(served, { method: 'POST', path: '/panel/api/server/importDB' })!;
    const upload = getRequest(served, op)!;
    expect(upload.content[0].mediaType).toBe('multipart/form-data');
    expect(upload.content[0].fields.find((field) => field.name === 'db')).toMatchObject({ type: 'binary', required: true });
    expect(upload.content[0].examples).toEqual([]);
    const form = getRequest(served, getOperation(served, { method: 'POST', path: '/panel/api/server/setUpdateChannel' })!)!;
    expect(form.content[0].mediaType).toBe('application/x-www-form-urlencoded');
    expect(form.content[0].fields.find((field) => field.name === 'dev')?.type).toBe('boolean');
    const doc = document({ paths: { '/upload': { post: { requestBody: { content: {
      'application/json': { schema: { type: 'object' }, example: null },
      'multipart/form-data': { schema: { type: 'object', properties: { tags: { type: 'array', items: { type: 'string' } } } }, encoding: { tags: { style: 'form', explode: true } } },
    } } } } } });
    const request = getRequest(doc, getOperation(doc, { method: 'POST', path: '/upload' })!)!;
    expect(request.content[0].examples[0].value).toBeNull();
    expect(request.content[1]).toMatchObject({ encoding: { tags: { style: 'form', explode: true } } });
  });

  it('preserves binary/text examples, response headers, and explicit public security overrides', () => {
    const doc = document({ security: [{ bearerAuth: [] }], servers: [{ url: '/prefix' }], paths: { '/file': { get: { security: [], responses: {
      '200': { description: 'File', headers: { 'Content-Disposition': { schema: { type: 'string' }, example: 'attachment' } }, content: { 'application/octet-stream': { schema: { type: 'string', format: 'binary' } }, 'text/plain': { schema: { type: 'string' }, example: 'raw file\nline 2' } } },
      '404': { description: 'Missing' },
    } } } } });
    const entry = buildApiCatalogue(doc, [])[0];
    const detail = getOperationDetails(doc, entry)!;
    expect(detail.security).toEqual([]);
    expect(detail.servers).toEqual([{ url: '/prefix' }]);
    expect(detail.responses[0].content[0]).toMatchObject({ isBinary: true, examples: [] });
    expect(detail.responses[0].content[1].examples[0].value).toBe('raw file\nline 2');
    expect(detail.responses[0].headers[0]).toMatchObject({ name: 'Content-Disposition', in: 'header', examples: [{ value: 'attachment' }] });
  });

  it('does not interpret literal $ref fields inside generated examples as documentation references', () => {
    const value = { $ref: '#/missing', nested: { $ref: 'https://example.invalid/example' } };
    const doc = document({ components: { examples: { Literal: { value } } }, paths: { '/example': { get: { responses: { '200': { content: { 'application/json': { examples: { named: { $ref: '#/components/examples/Literal' } } } } } } } } } });
    const example = getResponses(doc, getOperation(doc, { method: 'GET', path: '/example' })!)[0].content[0].examples[0];
    expect(example.value).toEqual(value);
  });
});

describe('bounded local references and safe text', () => {
  it('surfaces external, missing, cycle and budget diagnostics in operation metadata', () => {
    const doc = document({ paths: { '/diagnostics': { post: {
      parameters: [{ $ref: '#/components/parameters/Missing' }],
      requestBody: { content: { 'application/json': { schema: { $ref: 'https://example.invalid/schema' } } } },
      responses: { '200': { content: { 'application/json': { schema: { $ref: '#/components/schemas/Node' } } } }, '400': { content: { 'application/json': { schema: {
        type: 'object', properties: Object.fromEntries(Array.from({ length: 400 }, (_, i) => [`field${i}`, { type: 'string' }])),
      } } } } },
    } } }, components: { schemas: { Node: { type: 'array', items: { $ref: '#/components/schemas/Node' } } } } });
    const detail = getOperationDetails(doc, buildApiCatalogue(doc, [])[0])!;
    expect(detail.issues.map((issue) => issue.code)).toEqual(expect.arrayContaining(['missing', 'external', 'cycle', 'limit']));
    expect(detail.issues.every((issue) => issue.path.startsWith('$/'))).toBe(true);
    expect(detail.requestBody?.content[0].schema).toEqual({ $ref: 'https://example.invalid/schema' });
    expect(detail.responses[1].content[0].fields.length).toBeLessThanOrEqual(300);
  });

  it('resolves referenced path items, bodies, responses and external example pointers without fetching', () => {
    const doc = document({ 'x-paths': { item: { get: { responses: { '200': { $ref: '#/components/responses/Result' } } }, post: { requestBody: { $ref: '#/components/requestBodies/Input' } } } },
      paths: { '/alias': { $ref: '#/x-paths/item' } }, components: {
        requestBodies: { Input: { content: { 'application/json': { schema: { $ref: '#/components/schemas/Number' }, example: 0 } } } },
        responses: { Result: { content: { 'application/json': { examples: { sample: { $ref: '#/components/examples/External' } } } } } },
        examples: { External: { externalValue: 'https://example.invalid/data.json', summary: 'External sample' } },
        schemas: { Number: { type: ['integer', 'null'] } },
      },
    });
    expect(buildApiCatalogue(doc, []).map((entry) => entry.method)).toEqual(['GET', 'POST']);
    const body = getRequest(doc, getOperation(doc, { method: 'POST', path: '/alias' })!)!;
    expect(body.content[0].schema).toEqual({ type: ['integer', 'null'] });
    expect(body.content[0].examples[0].value).toBe(0);
    expect(body.content[0].fields[0].nullable).toBe(true);
    const response = getResponses(doc, getOperation(doc, { method: 'GET', path: '/alias' })!)[0];
    expect(response.content[0].examples).toEqual([{ name: 'sample', externalValue: 'https://example.invalid/data.json', summary: 'External sample' }]);
  });

  it('resolves escaped JSON pointers, sibling annotations, shared refs and false schemas', () => {
    const doc = document({ components: { schemas: { 'A/B~C': { type: 'integer' }, False: false } } });
    expect(resolveLocalRef(doc, { $ref: '#/components/schemas/A~1B~0C', description: 'ID' })).toEqual({ value: { type: 'integer', description: 'ID' }, issues: [] });
    expect(resolveLocalRef(doc, { $ref: '#/components/schemas/False' })).toEqual({ value: false, issues: [] });
    const shared = resolveLocalRef(doc, { properties: { a: { $ref: '#/components/schemas/A~1B~0C' }, b: { $ref: '#/components/schemas/A~1B~0C' } } });
    expect(shared.issues).toEqual([]);
    expect(shared.value).toEqual({ properties: { a: { type: 'integer' }, b: { type: 'integer' } } });
  });

  it('resolves property names that coincide with example/default annotation keywords', () => {
    const doc = document({ components: { schemas: { Number: { type: 'integer' } } } });
    const result = resolveLocalRef(doc, { type: 'object', properties: {
      example: { $ref: '#/components/schemas/Number' }, default: { $ref: '#/components/schemas/Number' },
    }, example: { default: { $ref: '#/literal-data' } } });
    expect(result.issues).toEqual([]);
    expect(result.value).toEqual({ type: 'object', properties: { example: { type: 'integer' }, default: { type: 'integer' } }, example: { default: { $ref: '#/literal-data' } } });
  });

  it('reports missing/external/prototype/cyclic references and limits recursive schema expansion', () => {
    const doc = document({ components: { schemas: { Node: { type: 'object', properties: { next: { $ref: '#/components/schemas/Node' } } } } } });
    for (const [ref, code] of [['#/missing', 'missing'], ['https://example.invalid/schema', 'external'], ['#/__proto__/polluted', 'missing'], ['#/%ZZ', 'invalid']]) {
      expect(resolveLocalRef(doc, { $ref: ref }).issues[0].code).toBe(code);
    }
    expect(resolveLocalRef(doc, { $ref: '#/components/schemas/Node' }).issues.some((issue) => issue.code === 'cycle')).toBe(true);
    const limited = resolveLocalRef(doc, { properties: Object.fromEntries(Array.from({ length: 500 }, (_, i) => [`field${i}`, { type: 'string' }])) }, { maxNodes: 20 });
    expect(limited.issues.some((issue) => issue.code === 'limit')).toBe(true);
    expect(JSON.stringify(limited.value).length).toBeLessThan(1000);
    const fields = schemaFields(doc, { type: 'array', items: { $ref: '#/components/schemas/Node' } });
    expect(fields.some((field) => field.path === '$[].next')).toBe(true);
    expect(fields.length).toBeLessThan(20);
  });

  it('returns documentation as text and preserves falsy and multiline examples', () => {
    expect(inlineDocText('Use <code>ws://<panel>/ws</code> with <strong>cookies</strong>.')).toBe('Use ws://<panel>/ws with cookies.');
    expect(inlineDocText('<script>alert(1)</script>')).toBe('<script>alert(1)</script>');
    expect(formatApiExample(false)).toBe('false');
    expect(formatApiExample(0)).toBe('0');
    expect(formatApiExample(null)).toBe('null');
    expect(formatApiExample('line1\nline2')).toBe('line1\nline2');
    expect(formatApiExample(undefined)).toBe('');
  });
});
