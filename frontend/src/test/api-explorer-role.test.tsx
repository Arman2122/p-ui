import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { QueryClient } from '@tanstack/react-query';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import i18next from 'i18next';
import type { SignedInUser } from '@/pages/resellers/schema';
import ApiExplorer from '@/pages/api-docs/ApiExplorer';
import { OpenApiDocumentSchema, normalizeApiPath, type OpenApiDocument } from '@/pages/api-docs/api-document';
import { sections } from '@/pages/api-docs/endpoints';
import { ClipboardManager, HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

const text = (key: string) => String(i18next.t(key));
const clients: QueryClient[] = [];
const widgets = '/panel/api/test/widgets/{id}';
const ownKeys = ['GET /panel/api/me', 'GET /panel/api/activity', 'POST /panel/api/activity/read', 'GET /panel/api/activity/alerts', 'GET /panel/api/reseller/clients', 'GET /panel/api/reseller/clients/{id}/links'];

function identity(role: 'admin' | 'reseller' = 'admin', plans = false, id = role === 'admin' ? 1 : 2): SignedInUser {
  return { id, username: `${role}-${id}`, role, home: '/', permissions: { plans, reset: false, extend: false, suspend: false, topUp: false } };
}

function documentFixture(marker = ''): OpenApiDocument {
  const response = { description: 'Successful result', content: { 'application/json': { schema: { $ref: '#/components/schemas/Result' }, example: { enabled: false, label: 'Result fixture' } } } };
  const read = { summary: 'Read account data', responses: { '200': response } };
  return OpenApiDocumentSchema.parse({ openapi: '3.0.3', info: { title: 'Unrelated global introduction', version: 'test' },
    tags: [{ name: 'Test operations', description: 'Unrelated global tag description' }],
    components: { schemas: { Result: { type: 'object', required: ['enabled'], properties: { enabled: { type: 'boolean' }, label: { type: 'string', description: 'Readable result label' } } } } },
    paths: {
      '/panel/api/me': { get: { ...read, parameters: [{ name: 'note', in: 'query', schema: { type: 'string' } }] } }, '/panel/api/activity': { get: read }, '/panel/api/activity/read': { post: read }, '/panel/api/activity/alerts': { get: read },
      '/panel/api/reseller/clients': { get: read }, '/panel/api/reseller/clients/{id}/links': { get: read }, '/panel/api/reseller/plans/list': { get: read },
      '/panel/api/resellers': { get: read }, '/panel/api/setting/apiTokens': { get: read }, '/panel/api/clients/list': { get: read },
      '/panel/api/clients/list/paged': { get: read }, '/panel/api/server/status': { get: read },
      '/panel/api/reseller-admin': { get: read }, '/panel/api/activity/admin': { get: read }, '/ws': { get: { summary: 'Session WebSocket handshake', responses: { '101': { description: 'Switching protocols' } } } },
      [widgets]: { parameters: [{ name: 'id', in: 'path', required: true, schema: { type: 'integer' }, example: 4 }, { name: 'q', in: 'query', schema: { type: 'string' } }],
        get: { summary: 'List widgets', tags: ['Test operations'], responses: { '200': response } },
        post: { summary: 'Update widgets', description: 'Edit the <code>widget</code> record.', tags: ['Test operations'], deprecated: true,
          requestBody: { required: true, content: { 'application/json': { schema: { $ref: '#/components/schemas/Result' }, example: { enabled: false, label: 'New widget' } }, 'application/problem+json': { schema: { $ref: '#/components/schemas/Result' } } } },
          responses: { '200': response, '403': { description: 'Owner permission denied' } } } },
      '/panel/api/test/summary-match': { get: { summary: 'Inspect audit metadata', tags: ['Ranked results'], responses: { '200': response } } },
      '/panel/api/test/inspect': { get: { summary: 'Inspect another operation', tags: ['Test operations'], responses: { '200': response } } },
      '/panel/api/test/upload': { post: { summary: 'Upload a fixture archive', tags: ['Uploads'], requestBody: { content: { 'multipart/form-data': { schema: { type: 'object', required: ['archive'], properties: { archive: { type: 'string', format: 'binary' }, note: { type: 'string' } } } } } }, responses: { '200': response } } },
      ...(marker ? { [`/panel/api/reseller/${marker}`]: { get: { summary: marker, responses: { '200': response } } } } : {}),
    },
    'x-websocket-messages': ['status', 'xray_state', 'notification', 'invalidate'].map(type => ({ type, summary: `Read <code>${type}</code> and <code>payload</code> events.`, example: { type, payload: { fixture: type }, time: 1 } })),
  });
}

function LocationProbe() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output data-testid="api-location">{location.pathname}{location.search}{location.hash}</output><button onClick={() => navigate(-1)}>Test history back</button><button onClick={() => navigate(1)}>Test history forward</button></>;
}

function setup({ user = identity(), spec = documentFixture(), url = '/api-docs?retain=yes', failUser = false }: { user?: SignedInUser | null; spec?: OpenApiDocument; url?: string; failUser?: boolean } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  if (user) client.setQueryData(['signed-in-user'], user);
  let rejected = failUser;
  let resolveMe: (value: Msg<unknown>) => void = () => {};
  const pendingMe = new Promise<Msg<unknown>>(resolve => { resolveMe = resolve; });
  vi.spyOn(HttpUtil, 'get').mockImplementation(async () => {
    if (rejected) { rejected = false; return new Msg(false, 'Identity unavailable'); }
    return user ? new Msg(true, '', user) : pendingMe;
  });
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response(JSON.stringify(spec), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const view = renderWithProviders(<MemoryRouter initialEntries={[url]}><ApiExplorer /><LocationProbe /></MemoryRouter>, { queryClient: client });
  return { ...view, client, fetch, resolveMe };
}

function catalogueKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[data-api-key]')].map(row => row.getAttribute('data-api-key')!).sort();
}

function entry(container: HTMLElement, key: string): HTMLElement {
  const row = [...container.querySelectorAll<HTMLElement>('[data-api-key]')].find(element => element.dataset.apiKey === key);
  expect(row, `Catalogue row ${key}`).toBeDefined();
  return row!;
}

async function loaded(container: HTMLElement) { await waitFor(() => expect(container.querySelector('[data-api-key]')).not.toBeNull()); }
function search(value: string) { fireEvent.change(screen.getByRole('textbox', { name: text('design.apiSearch') }), { target: { value } }); }
function selectOption(label: string, value: string) {
  const input = screen.getByRole('combobox', { name: label });
  fireEvent.mouseDown(input.closest('.ant-select')!);
  const option = [...document.querySelectorAll<HTMLElement>('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')].find(item => item.getAttribute('title') === value || item.textContent === value);
  expect(option, `Option ${value}`).toBeDefined();
  fireEvent.click(option!);
}
function currentParams() { return new URL(screen.getByTestId('api-location').textContent!, 'http://panel.test').searchParams; }
function openRequestBody() {
  const editor = document.querySelector<HTMLElement>('.p-apirequest-editor')!;
  fireEvent.click(within(editor).getByRole('tab', { name: text('apiRepair.request.body') }));
}
function quickstart(key: string) {
  const shortcuts = document.querySelector<HTMLElement>('.api-quickstart')!;
  fireEvent.click(within(shortcuts).getByRole('button', { name: text(key) }));
}
function chooseOperation(name: string) {
  const nav = screen.getByRole('navigation', { name: text('apiExplorer.operations') });
  const button = within(nav).getByRole('button', { name, hidden: true });
  const group = button.closest('details');
  if (group && !group.open) fireEvent.click(group.querySelector('summary')!);
  fireEvent.click(button);
}
function recentChoices() {
  const disclosure = document.querySelector<HTMLDetailsElement>('.api-recent');
  if (disclosure && !disclosure.open) fireEvent.click(disclosure.querySelector('summary')!);
  const recent = screen.queryByLabelText(text('apiUx.recent'));
  return recent ? [...recent.querySelectorAll('button')].map(button => button.getAttribute('aria-label')) : [];
}

afterEach(() => { clients.splice(0).forEach(client => client.clear()); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('native API catalogue and authenticated role', () => {
  it('waits for the real identity query before revealing or fetching APIs', async () => {
    const view = setup({ user: null });
    expect(catalogueKeys(view.container)).toEqual([]);
    expect(view.fetch).not.toHaveBeenCalled();
    await act(async () => { view.resolveMe(new Msg(true, '', identity('reseller'))); });
    await loaded(view.container);
    expect(catalogueKeys(view.container)).toEqual([...ownKeys].sort());
  });

  it('retries an identity failure without first requesting a document', async () => {
    const view = setup({ user: null, failUser: true });
    await screen.findByText('Identity unavailable');
    expect(view.fetch).not.toHaveBeenCalled();
    view.resolveMe(new Msg(true, '', identity('reseller')));
    fireEvent.click(screen.getByRole('button', { name: text('refresh') }));
    await loaded(view.container);
    expect(catalogueKeys(view.container)).toEqual([...ownKeys].sort());
  });

  it('opens the complete administrator catalogue by default without duplicated rows or request execution', async () => {
    const view = setup();
    await loaded(view.container);
    const keys = catalogueKeys(view.container);
    const registryKeys = sections.flatMap(section => section.endpoints.map(endpoint => `${endpoint.method} ${normalizeApiPath(endpoint.path)}`));
    expect(keys).toEqual(expect.arrayContaining(registryKeys));
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).toContain(`POST ${widgets}`);
    expect((screen.getByRole('radio', { name: text('apiExplorer.catalogue') }) as HTMLInputElement).checked).toBe(true);
    expect(view.container.querySelectorAll('.p-eprow')).toHaveLength(0);
    expect(screen.getByRole('navigation', { name: text('apiExplorer.categories') })).toBeTruthy();
    expect(screen.queryByRole('button', { name: text('apiRepair.request.send') })).toBeNull();
    expect(view.fetch).toHaveBeenCalledTimes(1);
    expect(view.fetch).toHaveBeenCalledWith('/panel/api/openapi.json', expect.objectContaining({ credentials: 'same-origin', signal: expect.any(AbortSignal) }));
    expect(screen.getByRole('link', { name: new RegExp(text('design.apiDownload')) }).getAttribute('href')).toBe('/panel/api/openapi.json');
    expect(view.container.querySelector('.swagger-ui')).toBeNull();
  }, 10_000);

  it.each([false, true])('shows only served reseller methods with plans=%s despite a spoofed global role', async plans => {
    vi.stubGlobal('PUI_ROLE', 'admin');
    const view = setup({ user: identity('reseller', plans) });
    await loaded(view.container);
    expect(catalogueKeys(view.container)).toEqual([...ownKeys, ...(plans ? ['GET /panel/api/reseller/plans/list'] : [])].sort());
    expect(view.client.getQueryData(['openapi', 2, 'reseller', plans])).toBeDefined();
    search('admin');
    expect(catalogueKeys(view.container)).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: text('apiExplorer.resetFilters') }));
    expect(catalogueKeys(view.container)).not.toContain('POST /panel/api/reseller/clients');
  });

  it.each(['admin', 'reseller'] as const)('uses the authenticated %s token-management destination', async role => {
    const view = setup({ user: identity(role) });
    await loaded(view.container);
    fireEvent.click(screen.getByRole('button', { name: new RegExp(text('design.apiTokens')) }));
    expect(screen.getByTestId('api-location').textContent).toBe(role === 'admin' ? '/settings#tokens' : '/reseller/account');
  }, 10_000);

  it('intersects search, method and category filters and resets the empty state', async () => {
    const view = setup();
    await loaded(view.container);
    const original = catalogueKeys(view.container);
    fireEvent.click(screen.getByRole('button', { name: /^Test operations\s*3$/ }));
    expect(catalogueKeys(view.container)).toHaveLength(3);
    selectOption(text('apiExplorer.method'), 'POST');
    search('wIDgEts POST');
    expect(catalogueKeys(view.container)).toEqual([`POST ${widgets}`]);
    search('LIST');
    expect(catalogueKeys(view.container)).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: text('apiExplorer.resetFilters') }));
    expect(catalogueKeys(view.container)).toEqual(original);
  }, 10_000);

  it('expands filtered references with parameters, schemas, examples and errors and preserves state across views', async () => {
    const view = setup();
    await loaded(view.container);
    search('widgets');
    fireEvent.click(screen.getByRole('button', { name: text('apiExplorer.expandAll') }));
    const post = entry(view.container, `POST ${widgets}`);
    expect(post.querySelector('details')?.open).toBe(true);
    for (const value of ['Readable result label', 'Owner permission denied', 'New widget', 'Result fixture', text('apiExplorer.deprecated')]) expect(post.textContent).toContain(value);
    expect(post.textContent).not.toContain('<code>');
    expect(post.querySelectorAll('code').length).toBeGreaterThan(1);
    fireEvent.click(within(post).getByRole('button', { name: text('apiExplorer.openExplorer') }));
    expect(currentParams().get('view')).toBe('explorer');
    expect(currentParams().get('endpoint')).toBe(`POST ${widgets}`);
    expect(currentParams().get('retain')).toBe('yes');
    expect(screen.queryByRole('navigation', { name: text('apiExplorer.categories') })).toBeNull();
    expect(screen.getByRole('combobox', { name: text('apiExplorer.categories') })).toBeTruthy();
    expect((screen.getByRole('textbox', { name: text('design.apiSearch') }) as HTMLInputElement).value).toBe('widgets');
    fireEvent.click(screen.getByRole('radio', { name: text('apiExplorer.catalogue') }));
    expect(entry(view.container, `POST ${widgets}`).querySelector('details')?.open).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: text('apiExplorer.collapseAll') }));
    expect([...view.container.querySelectorAll<HTMLDetailsElement>('article details')].every(details => !details.open)).toBe(true);
  });

  it('keeps the selected editor and path draft when filters hide its endpoint until an explicit choice', async () => {
    const view = setup({ url: `/api-docs?retain=yes&view=explorer&endpoint=${encodeURIComponent(`POST ${widgets}`)}` });
    const id = await screen.findByLabelText('path: id');
    fireEvent.change(id, { target: { value: '17' } });
    expect(view.container.querySelectorAll('[data-api-key]')).toHaveLength(0);
    selectOption(text('apiExplorer.method'), 'GET');
    search('no-such-operation');
    expect(screen.getByLabelText('path: id')).toBe(id);
    expect((id as HTMLInputElement).value).toBe('17');
    expect(currentParams().get('endpoint')).toBe(`POST ${widgets}`);
    search('Inspect another');
    expect((screen.getByLabelText('path: id') as HTMLInputElement).value).toBe('17');
    fireEvent.click(screen.getByRole('button', { name: 'GET /panel/api/test/inspect' }));
    expect(currentParams().get('endpoint')).toBe('GET /panel/api/test/inspect');
    expect(currentParams().get('retain')).toBe('yes');
    expect(screen.queryByLabelText('path: id')).toBeNull();
    expect(view.fetch).toHaveBeenCalledTimes(1);
  });

  it('shows all four WS payloads as native references without raw code markup or HTTP execution', async () => {
    const view = setup();
    await loaded(view.container);
    fireEvent.click(screen.getByRole('button', { name: /^WebSocket\s*5$/ }));
    fireEvent.click(screen.getByRole('button', { name: text('apiExplorer.expandAll') }));
    for (const type of ['status', 'xray_state', 'notification', 'invalidate']) {
      const row = entry(view.container, `WS → type: ${type}`);
      expect(JSON.parse(row.querySelector('pre')!.textContent!)).toMatchObject({ type, payload: { fixture: type }, time: 1 });
      expect(row.textContent).not.toMatch(/<\/?code>/);
      expect(row.querySelector('summary code')?.textContent).toBe(`→ type: ${type}`);
      expect(row.querySelector('summary p')?.textContent).toBe(`Read ${type} and payload events.`);
    }
    fireEvent.click(within(entry(view.container, 'WS → type: status')).getByRole('button', { name: text('apiExplorer.viewEvent') }));
    expect(screen.getByRole('heading', { name: 'status' })).toBeTruthy();
    expect(view.container.querySelector('.api-event-reference')?.textContent).toContain('new WebSocket(');
    expect(view.container.querySelector('.api-event-reference')?.textContent).not.toMatch(/<\/?code>/);
    expect(view.container.querySelector('.api-event-intro p')?.textContent).toBe('Read status and payload events.');
    expect(screen.queryByRole('button', { name: text('apiRepair.request.send') })).toBeNull();
    expect(screen.queryByText(text('apiExplorer.referenceOnly'))).toBeNull();
    expect(view.fetch).toHaveBeenCalledTimes(1);
  }, 10_000);

  it('shows grouped readable choices while preserving canonical paths for request URLs and shared links', async () => {
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    const view = setup({ url: `/api-docs?view=explorer&endpoint=${encodeURIComponent(`GET ${widgets}`)}` });
    fireEvent.change(await screen.findByLabelText('path: id'), { target: { value: '4' } });
    const button = screen.getByRole('button', { name: `GET ${widgets}` });
    expect(button.closest('details')?.open).toBe(true);
    expect(button.querySelector('code')?.textContent).toBe('/test/widgets/{id}');
    expect(button.querySelector('code')?.title).toBe(widgets);
    expect(button.querySelector('.api-nav-entry-summary')?.textContent).toBe('List widgets');
    fireEvent.click(screen.getByRole('button', { name: text('apiRepair.request.copyUrl') }));
    await waitFor(() => expect(copy).toHaveBeenCalled());
    expect(new URL(String(copy.mock.calls[0][0])).pathname).toBe('/panel/api/test/widgets/4');
    fireEvent.click(screen.getByRole('radio', { name: text('apiExplorer.catalogue') }));
    fireEvent.click(within(entry(view.container, `POST ${widgets}`)).getByRole('button', { name: text('apiExplorer.copyLink') }));
    await waitFor(() => expect(copy).toHaveBeenCalledTimes(2));
    expect(new URL(String(copy.mock.calls[1][0])).searchParams.get('endpoint')).toBe(`POST ${widgets}`);
    expect(view.fetch).toHaveBeenCalledTimes(1);
  });

  it.each(['admin', 'reseller'] as const)('quickstart selects only served authorised GET operations for %s without sending', async role => {
    const view = setup({ user: identity(role) });
    await loaded(view.container);
    quickstart('apiUx.startClients');
    expect(currentParams().get('endpoint')).toBe(role === 'admin' ? 'GET /panel/api/clients/list/paged' : 'GET /panel/api/reseller/clients');
    quickstart('apiUx.startIdentity');
    expect(currentParams().get('endpoint')).toBe('GET /panel/api/me');
    if (role === 'admin') {
      quickstart('apiUx.startStatus');
      expect(currentParams().get('endpoint')).toBe('GET /panel/api/server/status');
    } else expect(screen.queryByRole('button', { name: new RegExp(text('apiUx.startStatus')) })).toBeNull();
    expect(view.fetch).toHaveBeenCalledTimes(1);
  }, 10_000);

  it('bounds recent choices to six distinct IDs and reorders them when revisited', async () => {
    const view = setup({ url: '/api-docs?view=explorer' });
    await screen.findByLabelText('query: note');
    const visited = ['GET /panel/api/me', 'GET /panel/api/activity', 'GET /panel/api/reseller/clients', 'GET /panel/api/clients/list', 'GET /panel/api/server/status', 'GET /panel/api/test/inspect', 'GET /panel/api/setting/apiTokens'];
    for (const name of visited) chooseOperation(name);
    expect(recentChoices()).toEqual(visited.slice(1).reverse().map(name => `${text('apiUx.openRecent')}: ${name}`));
    const reused = visited[3];
    fireEvent.click(screen.getByRole('button', { name: `${text('apiUx.openRecent')}: ${reused}` }));
    expect(recentChoices()).toHaveLength(6);
    expect(recentChoices()[0]).toBe(`${text('apiUx.openRecent')}: ${reused}`);
    expect(new Set(recentChoices()).size).toBe(6);
    expect(currentParams().get('endpoint')).toBe(reused);
    expect(view.fetch).toHaveBeenCalledTimes(1);
  }, 10_000);

  it('keeps path, query, media type and invalid JSON drafts through explicit navigation and browser history', async () => {
    const view = setup({ url: `/api-docs?retain=yes&view=explorer&endpoint=${encodeURIComponent(`POST ${widgets.replace('{id}', ':id')}`)}` });
    fireEvent.change(await screen.findByLabelText('path: id'), { target: { value: '17' } });
    fireEvent.change(screen.getByLabelText('query: q'), { target: { value: 'kept/query value' } });
    openRequestBody();
    selectOption(text('apiRepair.request.contentType'), 'application/problem+json');
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }));
    const raw = '{"enabled": false, "label": "unfinished owner draft"';
    fireEvent.change(screen.getByLabelText(text('apiRepair.request.jsonBody')), { target: { value: raw } });
    chooseOperation('GET /panel/api/test/inspect');
    fireEvent.click(screen.getByRole('button', { name: 'Test history back' }));
    expect((await screen.findByLabelText('path: id') as HTMLInputElement).value).toBe('17');
    expect((screen.getByLabelText('query: q') as HTMLInputElement).value).toBe('kept/query value');
    openRequestBody();
    expect((screen.getByLabelText(text('apiRepair.request.jsonBody')) as HTMLTextAreaElement).value).toBe(raw);
    expect(screen.getByRole('combobox', { name: text('apiRepair.request.contentType') }).closest('.ant-select')?.textContent).toContain('application/problem+json');
    fireEvent.click(screen.getByRole('button', { name: 'Test history forward' }));
    expect(currentParams().get('endpoint')).toBe('GET /panel/api/test/inspect');
    expect(currentParams().get('retain')).toBe('yes');
    expect(view.fetch).toHaveBeenCalledTimes(1);
  });

  it('retains same-endpoint file choices on auth switches but requires reselecting files after endpoint navigation', async () => {
    const view = setup({ url: `/api-docs?view=explorer&endpoint=${encodeURIComponent('POST /panel/api/test/upload')}` });
    const upload = await screen.findByLabelText('body: archive');
    fireEvent.change(upload, { target: { files: [new File(['fixture'], 'fixture.archive')] } });
    fireEvent.change(screen.getByLabelText('body: note'), { target: { value: 'keep note only' } });
    selectOption(text('apiExplorer.sendAs'), text('apiExplorer.bearer'));
    fireEvent.change(screen.getByLabelText(text('apiExplorer.bearer'), { selector: 'input' }), { target: { value: 'memory-token' } });
    expect(screen.getByLabelText('body: archive')).toBe(upload);
    expect((upload as HTMLInputElement).files?.[0].name).toBe('fixture.archive');
    chooseOperation('GET /panel/api/me');
    chooseOperation('POST /panel/api/test/upload');
    expect((screen.getByLabelText('body: archive') as HTMLInputElement).files).toHaveLength(0);
    expect((screen.getByLabelText('body: note') as HTMLInputElement).value).toBe('keep note only');
    expect(screen.getByText(text('apiUx.reselectFiles'))).toBeTruthy();
    expect(view.fetch).toHaveBeenCalledTimes(1);
  });

  it('focuses search with slash, clears only search with Escape, and selects a ranked result only with Enter', async () => {
    const view = setup({ url: `/api-docs?view=explorer&endpoint=${encodeURIComponent(`POST ${widgets}`)}` });
    const id = await screen.findByLabelText('path: id');
    fireEvent.change(id, { target: { value: '23' } });
    const find = screen.getByRole('textbox', { name: text('design.apiSearch') });
    id.focus(); fireEvent.keyDown(id, { key: '/' });
    expect(document.activeElement).toBe(id);
    fireEvent.keyDown(window, { key: '/' });
    expect(document.activeElement).toBe(find);
    expect((find as HTMLInputElement).value).toBe('');
    fireEvent.keyDown(find, { key: 'Enter' });
    expect(currentParams().get('endpoint')).toBe(`POST ${widgets}`);
    search('Inspect another');
    fireEvent.keyDown(find, { key: 'Escape' });
    expect((find as HTMLInputElement).value).toBe('');
    expect((screen.getByLabelText('path: id') as HTMLInputElement).value).toBe('23');
    search('keep search'); id.focus(); fireEvent.keyDown(id, { key: 'Escape' });
    expect((find as HTMLInputElement).value).toBe('keep search');
    const editable = document.createElement('div');
    editable.contentEditable = 'true'; editable.setAttribute('contenteditable', 'true'); editable.tabIndex = 0;
    document.body.append(editable); editable.focus(); fireEvent.keyDown(editable, { key: '/' });
    expect(document.activeElement).toBe(editable); editable.remove();
    id.focus(); fireEvent.keyDown(window, { key: '/', ctrlKey: true });
    expect(document.activeElement).toBe(id);
    openRequestBody();
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }));
    const body = screen.getByLabelText(text('apiRepair.request.jsonBody'));
    body.focus(); fireEvent.keyDown(body, { key: '/' });
    expect(document.activeElement).toBe(body);
    search('inspect'); find.focus();
    expect(screen.getByRole('button', { name: 'GET /panel/api/test/summary-match' })).toBeTruthy();
    fireEvent.keyDown(find, { key: 'Enter' });
    expect(currentParams().get('endpoint')).toBe('GET /panel/api/test/inspect');
    expect(view.fetch).toHaveBeenCalledTimes(1);
  });

  it('retries a document failure and recovers the catalogue without showing a fake result', async () => {
    const view = setup({ user: null });
    view.fetch.mockResolvedValueOnce(new Response('unavailable', { status: 503, statusText: 'Unavailable' }));
    await act(async () => { view.resolveMe(new Msg(true, '', identity('reseller'))); });
    await screen.findByText('503 Unavailable');
    expect(catalogueKeys(view.container)).toEqual([]);
    fireEvent.click(screen.getByRole('button', { name: text('refresh') }));
    await loaded(view.container);
    expect(catalogueKeys(view.container)).toEqual([...ownKeys].sort());
  });

  it.each([
    ['role', identity(), identity('reseller')],
    ['account', identity('reseller', false, 2), identity('reseller', false, 3)],
    ['plans permission', identity('reseller', true), identity('reseller')],
  ] as const)('isolates cached documents and clears pasted tokens when the %s changes', async (_label, before, after) => {
    const view = setup({ user: before, spec: documentFixture('previous-owner-marker') });
    await loaded(view.container);
    const storage = vi.spyOn(Storage.prototype, 'setItem');
    selectOption(text('apiExplorer.sendAs'), text('apiExplorer.bearer'));
    fireEvent.change(screen.getByLabelText(text('apiExplorer.bearer'), { selector: 'input' }), { target: { value: 'private-token-before-change' } });
    expect((screen.getByLabelText(text('apiExplorer.bearer'), { selector: 'input' }) as HTMLInputElement).value).toBe('private-token-before-change');
    quickstart('apiUx.startIdentity');
    fireEvent.change(screen.getByLabelText('query: note'), { target: { value: 'previous-owner-note' } });
    chooseOperation('GET /panel/api/activity');
    chooseOperation('GET /panel/api/me');
    expect((screen.getByLabelText('query: note') as HTMLInputElement).value).toBe('previous-owner-note');
    expect(recentChoices()).toHaveLength(2);
    let finish: (value: Response) => void = () => {};
    view.fetch.mockImplementationOnce(() => new Promise<Response>(resolve => { finish = resolve; }));
    act(() => view.client.setQueryData(['signed-in-user'], after));
    await waitFor(() => expect(view.fetch).toHaveBeenCalledTimes(2));
    expect(catalogueKeys(view.container)).toEqual([]);
    expect(screen.queryByLabelText(text('apiExplorer.bearer'), { selector: 'input' })).toBeNull();
    expect(view.container.textContent).not.toContain('previous-owner-marker');
    await act(async () => { finish(new Response(JSON.stringify(documentFixture('next-owner-marker')))); });
    expect((await screen.findByLabelText('query: note') as HTMLInputElement).value).toBe('');
    expect(recentChoices()).toHaveLength(0);
    expect(storage.mock.calls.some(([, value]) => /previous-owner-note|private-token-before-change/.test(String(value)))).toBe(false);
    fireEvent.click(screen.getByRole('radio', { name: text('apiExplorer.catalogue') }));
    await loaded(view.container);
    expect(catalogueKeys(view.container)).toContain('GET /panel/api/reseller/next-owner-marker');
    expect(catalogueKeys(view.container)).not.toContain('GET /panel/api/reseller/previous-owner-marker');
    expect(catalogueKeys(view.container)).not.toContain('GET /panel/api/reseller/plans/list');
    expect(view.client.getQueryData(['openapi', after.id, after.role, after.permissions.plans])).toBeDefined();
    selectOption(text('apiExplorer.sendAs'), text('apiExplorer.bearer'));
    expect((screen.getByLabelText(text('apiExplorer.bearer'), { selector: 'input' }) as HTMLInputElement).value).toBe('');
  }, 10_000);

  it('ignores a previous identity document that completes after the new document', async () => {
    const view = setup({ user: null });
    let finishOld: (value: Response) => void = () => {};
    view.fetch.mockImplementationOnce(() => new Promise<Response>(resolve => { finishOld = resolve; }));
    await act(async () => { view.resolveMe(new Msg(true, '', identity())); });
    await waitFor(() => expect(view.fetch).toHaveBeenCalledTimes(1));
    const oldSignal = view.fetch.mock.calls[0][1]?.signal;
    act(() => view.client.setQueryData(['signed-in-user'], identity('reseller')));
    await loaded(view.container);
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => { finishOld(new Response(JSON.stringify(documentFixture('late-admin-marker')))); });
    expect(catalogueKeys(view.container)).toEqual([...ownKeys].sort());
    expect(view.container.textContent).not.toContain('late-admin-marker');
  });
});
