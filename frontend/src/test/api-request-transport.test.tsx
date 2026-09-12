import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OpenApiDocumentSchema, type OpenApiDocument } from '@/pages/api-docs/api-document';
import { initialRequestDraft, prepareApiRequest, validateApiValue, type ApiRequestDraft } from '@/pages/api-docs/request-inputs';
import { sendApiRequest, panelRequestUrl, type ApiPreparedRequest } from '@/pages/api-docs/request-transport';
import { apiRequestSnippet } from '@/pages/api-docs/request-snippets';
import type { Endpoint } from '@/pages/api-docs/endpoints';

const endpoint: Endpoint = { method: 'POST', path: '/panel/api/items/:id', summary: 'Update' };
function documentFor(operation: Record<string, unknown>, path = '/panel/api/items/{id}'): OpenApiDocument {
  return OpenApiDocumentSchema.parse({ openapi: '3.0.3', info: { title: 'test', version: '1' }, servers: [{ url: 'https://foreign.example' }], paths: { [path]: { post: operation, get: operation } } });
}
const parameters = [{ in: 'path', name: 'id', required: true, schema: { type: 'string' } }];
function draftFor(document: OpenApiDocument, overrides: Partial<ApiRequestDraft> = {}) {
  return { ...initialRequestDraft(document, endpoint), values: { 'path:id': 'a b' }, ...overrides };
}
function prepared(overrides: Partial<ApiPreparedRequest> = {}): ApiPreparedRequest {
  return { method: 'GET', url: `${window.location.origin}/secret/panel/api/items`, csrfUrl: `${window.location.origin}/secret/csrf-token`, headers: {}, auth: { mode: 'session' }, requiresCsrf: false, ...overrides };
}
beforeEach(() => { window.PUI_BASE_PATH = '/secret/'; });
afterEach(() => { delete window.PUI_BASE_PATH; vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('native API request preparation', () => {
  it('treats an empty caller base path as the configured root mount', () => {
    window.PUI_BASE_PATH = '/';
    expect(panelRequestUrl('/panel/api/me', '')).toBe(`${window.location.origin}/panel/api/me`);
    expect(panelRequestUrl('/panel/api/me', '/')).toBe(`${window.location.origin}/panel/api/me`);
  });

  it('accepts a normalized configured prefix and refuses a root override on that mount', () => {
    expect(panelRequestUrl('/panel/api/me', '/secret')).toBe(`${window.location.origin}/secret/panel/api/me`);
    expect(() => panelRequestUrl('/panel/api/me', '')).toThrow('apiRepair.request.invalidBase');
  });

  it('resolves path/query/header values under the real panel prefix and ignores foreign spec servers', () => {
    const doc = documentFor({ parameters: [...parameters,
      { in: 'query', name: 'tags', schema: { type: 'array', items: { type: 'string' } }, style: 'form', explode: false },
      { in: 'query', name: 'filter', schema: { type: 'object' }, style: 'deepObject' },
      { in: 'header', name: 'X-Trace', schema: { type: 'string' } },
    ] });
    const result = prepareApiRequest(doc, endpoint, draftFor(doc, { values: { 'path:id': 'a b', 'query:tags': '["one","two"]', 'query:filter': '{"active":false}', 'header:X-Trace': 'trace' } }), { mode: 'session' });
    expect(result.issues).toEqual([]);
    expect(result.request?.url).toBe(`${window.location.origin}/secret/panel/api/items/a%20b?tags=one%2Ctwo&filter%5Bactive%5D=false`);
    expect(result.request?.headers['X-Trace']).toBe('trace');
  });

  it.each(['..', '../escape', 'a/b', 'a\\b', '\n'])('rejects unsafe path value %j', (id) => {
    const doc = documentFor({ parameters });
    expect(prepareApiRequest(doc, endpoint, draftFor(doc, { values: { 'path:id': id } }), { mode: 'session' }).request).toBeUndefined();
  });

  it.each(['https://foreign.example', '//foreign.example', '/secret/../other', '/elsewhere'])('rejects base override %s', (base) => {
    expect(() => panelRequestUrl('/panel/api/items', base)).toThrow();
  });

  it('blocks missing paths, malformed integers, unsafe headers and unresolved request refs', () => {
    const doc = documentFor({ parameters: [...parameters, { in: 'query', name: 'limit', required: true, schema: { type: 'integer' } }, { in: 'header', name: 'X-Test', schema: { type: 'string' } }] });
    const bad = prepareApiRequest(doc, endpoint, draftFor(doc, { values: { 'query:limit': '1.5', 'header:X-Test': 'x\r\nInjected: yes' } }), { mode: 'session' });
    expect(bad.request).toBeUndefined();
    expect(bad.issues.map((issue) => issue.field)).toEqual(expect.arrayContaining(['id', 'limit', 'X-Test']));
    const missing = documentFor({ parameters, requestBody: { $ref: '#/components/requestBodies/missing' } });
    expect(prepareApiRequest(missing, endpoint, draftFor(missing), { mode: 'session' }).issues.some((issue) => issue.field === 'schema')).toBe(true);
  });

  it('validates JSON required/nested fields and preserves false and zero', () => {
    const doc = documentFor({ parameters, requestBody: { required: true, content: { 'application/json': { schema: { type: 'object', required: ['enabled', 'count'], properties: { enabled: { type: 'boolean' }, count: { type: 'integer', minimum: 0 } } } } } } });
    expect(prepareApiRequest(doc, endpoint, draftFor(doc, { json: '{' }), { mode: 'session' }).request).toBeUndefined();
    expect(prepareApiRequest(doc, endpoint, draftFor(doc, { json: '{}' }), { mode: 'session' }).request).toBeUndefined();
    const result = prepareApiRequest(doc, endpoint, draftFor(doc, { json: '{"enabled":false,"count":0}' }), { mode: 'session' });
    expect(result.issues).toEqual([]);
    expect(result.request?.body).toBe('{"enabled":false,"count":0}');
    expect(result.request?.requiresCsrf).toBe(true);
  });

  it('serializes form and file uploads without manufacturing multipart boundaries', () => {
    const operation = { parameters, requestBody: { content: { 'multipart/form-data': { schema: { type: 'object', required: ['archive', 'enabled'], properties: { archive: { type: 'string', format: 'binary' }, enabled: { type: 'boolean' }, count: { type: 'integer' } } } } } } };
    const doc = documentFor(operation), file = new File(['real upload content'], 'archive.dump');
    const result = prepareApiRequest(doc, endpoint, draftFor(doc, { values: { 'path:id': 'one', 'body:enabled': 'false', 'body:count': '0' }, files: { archive: [file] } }), { mode: 'bearer', token: 'pasted' });
    expect(result.issues).toEqual([]);
    expect(result.request?.headers['Content-Type']).toBeUndefined();
    expect((result.request?.body as FormData).get('enabled')).toBe('false');
    expect(((result.request?.body as FormData).get('archive') as File).name).toBe('archive.dump');
    expect(result.request?.requiresCsrf).toBe(false);
    const formDoc = documentFor({ parameters, requestBody: { content: { 'application/x-www-form-urlencoded': { schema: { type: 'object', properties: { values: { type: 'array', items: { type: 'integer' } } } } } } } });
    expect(prepareApiRequest(formDoc, endpoint, draftFor(formDoc, { values: { 'path:id': 'one', 'body:values': '[0,1]' } }), { mode: 'session' }).request?.body).toBe('values=0&values=1');
  });

  it('refuses bearer auth on cookie-session routes and rejects missing bearer tokens', () => {
    const doc = documentFor({ parameters });
    expect(prepareApiRequest(doc, endpoint, draftFor(doc), { mode: 'bearer', token: '' }).request).toBeUndefined();
    const login = { method: 'POST', path: '/login', summary: 'login' } as Endpoint;
    const loginDoc = documentFor({}, '/login');
    expect(prepareApiRequest(loginDoc, login, initialRequestDraft(loginDoc, login), { mode: 'bearer', token: 'pasted' }).issues[0].key).toBe('apiRepair.request.sessionOnly');
  });

  it('rejects malformed nested array data from resolved local request schemas', () => {
    const doc = documentFor({});
    doc.components = { schemas: { Item: { type: 'object', required: ['id'], properties: { id: { type: 'integer' } } } } };
    expect(validateApiValue(doc, { type: 'array', items: { $ref: '#/components/schemas/Item' } }, [{ id: 'bad' }])).toEqual([{ field: '$[0].id', key: 'apiRepair.request.invalidValue' }]);
  });
});

describe('real response transport', () => {
  it('fetches CSRF before unsafe session requests and preserves an HTTP 200 refusal', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(new Response('{"success":true,"obj":"csrf-secret"}')).mockResolvedValueOnce(new Response('{"success":false,"msg":"refused"}', { status: 200, headers: { 'content-type': 'application/json', 'x-trace': 'real' } }));
    vi.stubGlobal('fetch', fetcher);
    const result = await sendApiRequest(prepared({ method: 'POST', requiresCsrf: true, body: '{}' }), new AbortController().signal);
    expect(fetcher.mock.calls[0][0]).toBe(`${window.location.origin}/secret/csrf-token`);
    expect((fetcher.mock.calls[1][1].headers as Headers).get('X-CSRF-Token')).toBe('csrf-secret');
    expect(result.status).toBe(200); expect(result.envelopeFailed).toBe(true); expect(result.body).toBe('{"success":false,"msg":"refused"}');
    expect(result.headers).toContainEqual(['x-trace', 'real']);
    expect(result.size).toBe(new TextEncoder().encode(result.body).length);
  });

  it('never sends a mutation when CSRF acquisition fails and never retries a refused mutation', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('denied', { status: 403 }));
    vi.stubGlobal('fetch', fetcher);
    await expect(sendApiRequest(prepared({ method: 'POST', requiresCsrf: true }), new AbortController().signal)).rejects.toThrow('apiRepair.request.csrfFailed');
    expect(fetcher).toHaveBeenCalledTimes(1);
    fetcher.mockReset().mockResolvedValueOnce(new Response('{"success":true,"obj":"csrf"}')).mockResolvedValueOnce(new Response('forbidden', { status: 403, headers: { 'content-type': 'text/plain' } }));
    const result = await sendApiRequest(prepared({ method: 'POST', requiresCsrf: true }), new AbortController().signal);
    expect(fetcher).toHaveBeenCalledTimes(2); expect(result.status).toBe(403); expect(result.body).toBe('forbidden');
  });

  it('uses only the pasted bearer, omits cookies, blocks redirects, and retains binary responses', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(new Uint8Array([0, 1, 255]), { status: 200, headers: { 'content-type': 'application/octet-stream' } }));
    vi.stubGlobal('fetch', fetcher);
    const result = await sendApiRequest(prepared({ auth: { mode: 'bearer', token: 'provided-token' } }), new AbortController().signal);
    const options = fetcher.mock.calls[0][1];
    expect(options.credentials).toBe('omit'); expect(options.redirect).toBe('error'); expect(options.headers.get('Authorization')).toBe('Bearer provided-token');
    expect(result.kind).toBe('binary'); expect(result.size).toBe(3); expect(result.blob.size).toBe(3);
  });

  it('bounds captured bodies and labels incomplete data without inventing total bytes', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new Uint8Array(8 * 1024 * 1024 + 10), { headers: { 'content-type': 'application/octet-stream' } })));
    const result = await sendApiRequest(prepared(), new AbortController().signal);
    expect(result.truncated).toBe(true); expect(result.size).toBe(8 * 1024 * 1024); expect(result.declaredSize).toBeUndefined();
  });

  it('honours cancellation before sending and preserves empty 204 responses', async () => {
    const controller = new AbortController(); controller.abort();
    const fetcher = vi.fn().mockResolvedValue(new Response(null, { status: 204 })); vi.stubGlobal('fetch', fetcher);
    await expect(sendApiRequest(prepared(), controller.signal)).rejects.toThrow(); expect(fetcher).not.toHaveBeenCalled();
    const result = await sendApiRequest(prepared(), new AbortController().signal);
    expect(result.status).toBe(204); expect(result.size).toBe(0); expect(result.body).toBe('');
  });
});

describe('request snippets', () => {
  it('escapes shell payloads, produces valid JavaScript, and emits Python without JSON boolean literals', () => {
    const request = prepared({ method: 'POST', auth: { mode: 'bearer', token: "a'b" }, body: '{"enabled":false,"value":"it\'s $(touch never)"}', headers: { 'Content-Type': 'application/json' } });
    const curl = apiRequestSnippet('curl', request);
    expect(curl).toContain("a'\"'\"'b"); expect(curl).toContain('--data-raw');
    const js = apiRequestSnippet('JavaScript', request);
    expect(() => new Function(`return async function(){${js}}`)).not.toThrow();
    const python = apiRequestSnippet('Python', request);
    expect(python).toContain('data='); expect(python).toContain('.encode("utf-8")'); expect(python).not.toContain('json={');
  });

  it('builds session CSRF setup and multipart snippets without explicit boundary headers', () => {
    const request = prepared({ method: 'POST', requiresCsrf: true, mediaType: 'multipart/form-data', body: new FormData(), fields: [['file', new File(['content'], 'archive.dump')], ['note', 'literal @value']] });
    const curl = apiRequestSnippet('curl', request);
    expect(curl).toContain('COOKIE_JAR='); expect(curl).toContain('X-CSRF-Token: $CSRF'); expect(curl).toContain("--form-string 'note=literal @value'");
    expect(curl).not.toContain('Content-Type: multipart/form-data');
    expect(() => new Function(`return async function(){${apiRequestSnippet('JavaScript', request)}}`)).not.toThrow();
    expect(apiRequestSnippet('Python', request)).toContain('open("archive.dump", "rb")');
  });
});
