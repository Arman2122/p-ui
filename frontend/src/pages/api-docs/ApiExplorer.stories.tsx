import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { ClipboardManager, HttpUtil, Msg } from '@/utils';
import type { SignedInUser } from '@/pages/resellers/schema';
import { OpenApiDocumentSchema, type OpenApiDocument } from './api-document';
import { sections } from './endpoints';
import ApiExplorer from './ApiExplorer';

let source: OpenApiDocument;
const text = (key: string) => String(i18next.t(key));
const registryCount = sections.reduce((count, section) => count + section.endpoints.length, 0);

interface ApiStoryProps {
  role?: 'admin' | 'reseller';
  plans?: boolean;
  view?: 'catalogue' | 'explorer';
  endpoint?: string;
  failRead?: boolean;
  dark?: boolean;
}

function storyUser({ role = 'admin', plans = false }: ApiStoryProps): SignedInUser {
  return { id: role === 'admin' ? 1 : 2, username: role === 'admin' ? 'api-review' : 'reseller-review', role, home: '/', permissions: { plans, reset: false, extend: false, suspend: false, topUp: false } };
}

function storyDocument(user: SignedInUser): OpenApiDocument {
  if (user.role === 'admin') return source;
  return { ...source, 'x-websocket-messages': undefined, paths: Object.fromEntries(Object.entries(source.paths).filter(([path]) => {
    const allowed = path === '/panel/api/me' || /^\/panel\/api\/activity(?:\/(?:read|alerts))?$/.test(path) || path.startsWith('/panel/api/reseller/');
    return allowed && (user.permissions.plans || !/^\/panel\/api\/reseller\/plans(?:\/|$)/.test(path));
  })) };
}

function StorySurface({ dark = false }: { dark?: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  useTokenBridge();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [dark, isDark, toggleTheme]);
  return <main className="p-page"><ApiExplorer /></main>;
}

function ApiStory(props: ApiStoryProps) {
  const [client] = useState(() => {
    const cache = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    const user = storyUser(props);
    cache.setQueryData(['signed-in-user'], user);
    if (!props.failRead) cache.setQueryData(['openapi', user.id, user.role, user.permissions.plans], storyDocument(user));
    return cache;
  });
  useEffect(() => () => client.clear(), [client]);
  const params = new URLSearchParams();
  if (props.view === 'explorer') { params.set('view', 'explorer'); params.set('endpoint', props.endpoint ?? 'GET /panel/api/me'); }
  return <MemoryRouter initialEntries={[`/api-docs?${params}`]}><QueryClientProvider client={client}><ThemeProvider><StorySurface dark={props.dark} /></ThemeProvider></QueryClientProvider></MemoryRouter>;
}

function catalogueRows(canvasElement: HTMLElement): HTMLElement[] { return [...canvasElement.querySelectorAll<HTMLElement>('[data-api-key]')]; }
function assertNoOverflow(canvasElement: HTMLElement) {
  const page = canvasElement.querySelector<HTMLElement>('.api-docs-page')!;
  const geometry = [...page.children].map(child => ({ class: child.className, width: child.getBoundingClientRect().width, scroll: child.scrollWidth }));
  expect(page.scrollWidth, JSON.stringify(geometry)).toBeLessThanOrEqual(page.clientWidth + 1);
  for (const card of canvasElement.querySelectorAll<HTMLElement>('.api-reference-card')) expect(card.scrollWidth).toBeLessThanOrEqual(card.clientWidth + 1);
}

async function selectStoryOption(canvas: ReturnType<typeof within>, label: string, value: string) {
  const input = canvas.getByRole('combobox', { name: label });
  await userEvent.click(input.closest('.ant-select')!);
  const option = [...document.querySelectorAll<HTMLElement>('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')].find(item => item.title === value || item.textContent === value)!;
  await waitFor(() => expect(option).toBeVisible());
  await userEvent.click(option);
  await waitFor(() => expect(input).toHaveAttribute('aria-expanded', 'false'));
  await waitFor(() => {
    const listbox = within(document.body).queryByRole('listbox');
    if (listbox) expect(listbox).not.toBeVisible();
  }, { timeout: 2500 });
}

const meta = {
  title: 'Pages/API', component: ApiStory, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  render: (args, context) => <ApiStory {...args} dark={context.globals.theme === 'dark'} />,
  beforeEach: async context => {
    if (!source) {
      const response = await globalThis.fetch('/openapi.json');
      if (!response.ok) throw new Error(`Story OpenAPI asset unavailable: ${response.status}`);
      source = OpenApiDocumentSchema.parse(await response.json());
    }
    const user = storyUser(context.args);
    let failRead = context.args.failRead;
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      if (url === '/panel/api/me') return new Msg(true, '', user as T);
      throw new Error(`Unexpected story HTTP request: ${url}`);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async () => { throw new Error('Story attempted an unexpected mutation'); });
    const copy = spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    const fetch = spyOn(globalThis, 'fetch').mockImplementation(async input => {
      if (!String(input).endsWith('/panel/api/openapi.json')) throw new Error(`Story attempted request execution: ${String(input)}`);
      if (failRead) { failRead = false; return new Response('Unavailable', { status: 503, statusText: 'Unavailable' }); }
      return new Response(JSON.stringify(storyDocument(user)), { headers: { 'Content-Type': 'application/json' } });
    });
    return () => { get.mockRestore(); post.mockRestore(); fetch.mockRestore(); copy.mockRestore(); };
  },
} satisfies Meta<typeof ApiStory>;
export default meta;
type Story = StoryObj<typeof meta>;

export const AllApis: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(catalogueRows(canvasElement)).toHaveLength(registryCount));
    expect(canvas.getByRole('radio', { name: text('apiExplorer.catalogue') })).toBeChecked();
    expect(canvas.queryByRole('button', { name: new RegExp(text('apiRepair.request.send')) })).toBeNull();
    expect(canvasElement.querySelectorAll('.p-eprow')).toHaveLength(0);
    expect(new Set(catalogueRows(canvasElement).map(row => row.dataset.apiKey)).size).toBe(registryCount);
    assertNoOverflow(canvasElement);
  },
};

export const AllApisDark: Story = {
  ...AllApis, globals: { theme: 'dark' },
};

export const RequestExplorer: Story = {
  args: { view: 'explorer' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvasElement.querySelector('.p-apirequest')).toBeVisible());
    const mobile = window.innerWidth <= 760;
    const browse = mobile ? canvas.getByRole('button', { name: new RegExp(text('apiUx.browseEndpoints')) }) : undefined;
    let navigation = canvas;
    if (browse) {
      expect(browse).toHaveAttribute('aria-expanded', 'false');
      expect(canvas.queryByRole('navigation', { name: text('apiExplorer.operations') })).toBeNull();
      await userEvent.click(browse);
      const dialog = await within(document.body).findByRole('dialog');
      navigation = within(dialog);
      expect(navigation.getByText(text('apiUx.browseEndpoints'))).toBeVisible();
      const navigationSearch = navigation.getByRole('textbox', { name: text('apiUx.searchInNavigation') });
      expect(navigationSearch).toBeVisible();
      navigationSearch.focus(); await userEvent.keyboard('{Enter}');
      expect(browse).toHaveAttribute('aria-expanded', 'true');
    }
    expect(navigation.getByRole('combobox', { name: text('apiExplorer.categories') }).closest('.ant-select')).toBeVisible();
    expect(canvas.queryByRole('navigation', { name: text('apiExplorer.categories') })).toBeNull();
    expect(canvas.getByRole('button', { name: new RegExp(text('apiRepair.request.send')) })).toBeVisible();
    expect(canvas.getAllByText(text('apiRepair.request.unsentHint')).length).toBeGreaterThan(0);
    const activeOperation = navigation.getByRole('button', { name: 'GET /panel/api/me' });
    expect(activeOperation).toBeVisible();
    expect(activeOperation.closest('details')).toHaveAttribute('open');
    expect(activeOperation.querySelector('code')).toHaveTextContent('/me');
    expect(activeOperation.querySelector('code')).toHaveAttribute('title', '/panel/api/me');
    expect(activeOperation.querySelector('.api-nav-entry-summary')?.textContent?.length).toBeGreaterThan(0);
    await waitFor(() => {
      const nav = activeOperation.closest('nav')!;
      const navigationGeometry = { viewport: window.innerHeight, scrollY: window.scrollY, navTop: nav.getBoundingClientRect().top, navBottom: nav.getBoundingClientRect().bottom, height: nav.clientHeight, scrollTop: nav.scrollTop, scrollHeight: nav.scrollHeight, activeTop: activeOperation.getBoundingClientRect().top };
      expect(activeOperation.getBoundingClientRect().top, JSON.stringify(navigationGeometry)).toBeLessThan(window.innerHeight);
    });
    if (browse) {
      await userEvent.click(activeOperation);
      expect(browse).toHaveAttribute('aria-expanded', 'false');
      await waitFor(() => expect(within(document.body).queryByRole('dialog')).toBeNull());
      expect(canvas.queryByRole('navigation', { name: text('apiExplorer.operations') })).toBeNull();
    }
    const editor = canvasElement.querySelector('.p-apirequest-editor')!.getBoundingClientRect();
    const response = canvasElement.querySelector('.p-apirequest-response')!.getBoundingClientRect();
    if (window.innerWidth > 1050) expect(response.left).toBeGreaterThanOrEqual(editor.right - 1);
    else expect(response.top).toBeGreaterThanOrEqual(editor.bottom - 1);
    const responseTabs = within(canvasElement.querySelector<HTMLElement>('.p-apirequest-response')!);
    for (const key of ['responseBody', 'responseHeaders', 'responseExample']) expect(responseTabs.getByRole('tab', { name: text(`apiUx.${key}`) })).toBeVisible();
    await userEvent.click(responseTabs.getByRole('tab', { name: text('apiUx.responseExample') }));
    await waitFor(() => expect(canvas.getByText(text('apiRepair.request.exampleOnly'))).toBeVisible());
    await userEvent.click(responseTabs.getByRole('tab', { name: text('apiUx.responseBody') }));
    assertNoOverflow(canvasElement);
    if (browse) {
      await userEvent.click(browse);
      await waitFor(() => expect(within(document.body).getByRole('dialog')).toBeVisible());
    }
  },
};

export const ResellerApis: Story = {
  args: { role: 'reseller' },
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(catalogueRows(canvasElement).length).toBeGreaterThan(0));
    const keys = catalogueRows(canvasElement).map(row => row.dataset.apiKey!);
    expect(keys).toContain('GET /panel/api/me');
    expect(keys.some(key => key.includes('/panel/api/reseller/plans'))).toBe(false);
    expect(keys.some(key => key.startsWith('WS ') || key.endsWith(' /ws') || key.includes('/panel/api/server/'))).toBe(false);
    assertNoOverflow(canvasElement);
  },
};

export const ResellerWithPlans: Story = {
  args: { role: 'reseller', plans: true },
  play: async ({ canvasElement }) => {
    await waitFor(() => expect(catalogueRows(canvasElement).map(row => row.dataset.apiKey)).toContain('GET /panel/api/reseller/plans/list'));
    expect(catalogueRows(canvasElement).some(row => row.dataset.apiKey?.includes('/panel/api/resellers'))).toBe(false);
  },
};

export const SearchAndReset: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const search = canvas.getByRole('textbox', { name: text('design.apiSearch') });
    await userEvent.type(search, 'no-such-operation-xyz');
    expect(catalogueRows(canvasElement)).toHaveLength(0);
    await userEvent.click(canvas.getByRole('button', { name: text('apiExplorer.resetFilters') }));
    expect(catalogueRows(canvasElement)).toHaveLength(registryCount);
  },
};

export const WebSocketReference: Story = {
  args: { view: 'explorer', endpoint: 'WS → type: xray_state' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await canvas.findByRole('heading', { name: 'xray_state' });
    const detail = canvasElement.querySelector('.api-event-reference')!;
    expect(detail.textContent).not.toMatch(/<\/?code>/);
    expect(detail.textContent).toContain('state is running');
    const payload = [...detail.querySelectorAll('pre')].find(pre => pre.textContent?.trim().startsWith('{'))!;
    expect(JSON.parse(payload.textContent!)).toMatchObject({ type: 'xray_state', payload: { state: 'running' } });
    expect(canvas.queryByRole('button', { name: new RegExp(text('apiRepair.request.send')) })).toBeNull();
    assertNoOverflow(canvasElement);
  },
};

export const DocumentRetry: Story = {
  args: { role: 'reseller', failRead: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await canvas.findByText('503 Unavailable');
    expect(catalogueRows(canvasElement)).toHaveLength(0);
    await userEvent.click(canvas.getByRole('button', { name: text('refresh') }));
    await waitFor(() => expect(catalogueRows(canvasElement).length).toBeGreaterThan(0));
  },
};

export const NarrowCatalogue: Story = {
  ...AllApis,
  parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } },
};

export const NarrowExplorer: Story = {
  ...RequestExplorer,
  parameters: NarrowCatalogue.parameters,
};

export const NavigationAt764: Story = {
  ...RequestExplorer,
  name: 'Navigation at 764px',
  parameters: { viewport: { defaultViewport: 'boundary', viewports: { boundary: { name: 'Navigation boundary', styles: { width: '764px', height: '900px' }, type: 'tablet' } } } },
};

export const RequestExplorerDark: Story = { ...RequestExplorer, globals: { theme: 'dark' } };
export const NarrowExplorerDark: Story = { ...NarrowExplorer, globals: { theme: 'dark' } };

export const DraftWhileSearching: Story = {
  args: { view: 'explorer', endpoint: 'POST /panel/api/setting/update' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvasElement.querySelector('.p-apirequest')).toBeVisible());
    const bodyTab = within(canvasElement.querySelector<HTMLElement>('.p-apirequest-editor')!).queryByRole('tab', { name: text('apiRepair.request.body') });
    if (bodyTab) await userEvent.click(bodyTab);
    const json = canvas.queryByRole('radio', { name: 'JSON' });
    if (json) await userEvent.click(json.closest('label')!);
    const body = canvas.getByRole('textbox', { name: text('apiRepair.request.jsonBody') });
    await userEvent.clear(body); await userEvent.type(body, '{{"webPort":1234}', { skipClick: true });
    const value = (body as HTMLTextAreaElement).value;
    await userEvent.keyboard('/');
    expect(body).toHaveFocus();
    const draft = (body as HTMLTextAreaElement).value;
    expect(draft).toBe(`${value}/`);
    const headerButton = canvas.getByRole('button', { name: new RegExp(text('design.apiTokens')) });
    headerButton.focus(); await userEvent.keyboard('/');
    const search = canvas.getByRole('textbox', { name: text('design.apiSearch') });
    expect(search).toHaveFocus(); expect(search).toHaveValue('');
    await userEvent.type(search, 'no-such-operation-xyz');
    await selectStoryOption(canvas, text('apiExplorer.method'), 'GET');
    expect(canvas.getByText(text('apiUx.selectionOutsideSearch'))).toBeVisible();
    expect(canvas.getByRole('textbox', { name: text('apiRepair.request.jsonBody') })).toBe(body);
    expect(body).toHaveValue(draft);
    search.focus(); await userEvent.keyboard('{Escape}');
    expect(search).toHaveValue(''); expect(body).toHaveValue(draft);
    expect(canvas.getByRole('combobox', { name: text('apiExplorer.method') }).closest('.ant-select')).toHaveTextContent('GET');
    expect(canvas.getByText(text('apiUx.selectionOutsideSearch'))).toBeVisible();
    assertNoOverflow(canvasElement);
  },
};

export const NarrowDraftWhileSearching: Story = { ...DraftWhileSearching, parameters: NarrowCatalogue.parameters };

export const QuickstartAndRecents: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    for (const key of ['startIdentity', 'startClients', 'startStatus']) {
      await userEvent.click(canvas.getByRole('button', { name: text(`apiUx.${key}`) }));
    }
    const disclosure = canvasElement.querySelector<HTMLDetailsElement>('.api-recent')!;
    expect(disclosure.open).toBe(false);
    await userEvent.click(disclosure.querySelector('summary')!);
    const recent = canvas.getByLabelText(text('apiUx.recent'));
    const choices = within(recent).getAllByRole('button');
    expect(choices).toHaveLength(3);
    expect(choices[0]).toHaveAccessibleName(`${text('apiUx.openRecent')}: GET /panel/api/server/status`);
    await userEvent.click(choices[2]);
    expect(canvas.getByRole('button', { name: 'GET /panel/api/me' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(recent).getAllByRole('button')).toHaveLength(3);
    assertNoOverflow(canvasElement);
  },
};

export const LoginFields: Story = {
  args: { view: 'explorer', endpoint: 'POST /login' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const username = await canvas.findByLabelText('body: username');
    const password = canvas.getByLabelText('body: password');
    expect(username).toHaveValue(''); expect(password).toHaveValue('');
    expect(password).toHaveAttribute('type', 'password');
    expect(password).toHaveAttribute('autocomplete', 'new-password');
    const label = (username as HTMLInputElement).labels?.[0];
    expect(label).toBeDefined();
    await waitFor(() => expect(label).toBeVisible());
    expect(label!.getBoundingClientRect().bottom).toBeLessThanOrEqual(username.getBoundingClientRect().top + 1);
    await userEvent.click(label!); expect(username).toHaveFocus();
    await userEvent.type(username, 'storybook-user');
    await userEvent.tab(); expect(password).toHaveFocus();
    await userEvent.type(password, 'Story-only-password-42!');
    expect(username).toHaveValue('storybook-user');
    expect(password).toHaveValue('Story-only-password-42!');
    expect(canvasElement.querySelector('label label')).toBeNull();
    expect(canvas.getByText(text('apiRepair.request.unsentHint'))).toBeVisible();
    assertNoOverflow(canvasElement);
  },
};

async function expandedReference(canvasElement: HTMLElement, key: string) {
  const canvas = within(canvasElement);
  await userEvent.type(canvas.getByRole('textbox', { name: text('design.apiSearch') }), key);
  const row = catalogueRows(canvasElement).find(item => item.dataset.apiKey === key)!;
  expect(row).toBeDefined();
  const summary = row.querySelector<HTMLElement>('summary')!;
  await userEvent.click(summary);
  await waitFor(() => expect(row.querySelector('.api-reference-grid')).toBeVisible());
  expect(row.dataset.method).toBe(key.split(' ')[0]);
  const action = within(row).getByRole('button', { name: text('apiExplorer.openExplorer') });
  expect(action.getBoundingClientRect().top).toBeLessThan(summary.getBoundingClientRect().bottom + 1);
  const inputs = row.querySelector<HTMLElement>('.api-reference-inputs')!;
  const responses = within(row).getByRole('group', { name: text('apiExplorer.documentedResponses') });
  const width = row.querySelector<HTMLElement>('.api-reference-sections')!.clientWidth;
  if (width > 850) expect(responses.getBoundingClientRect().left).toBeGreaterThanOrEqual(inputs.getBoundingClientRect().right - 1);
  else expect(responses.getBoundingClientRect().top).toBeGreaterThanOrEqual(inputs.getBoundingClientRect().bottom - 1);
  return { canvas, row, responses };
}

async function nextReferenceExample(scope: ReturnType<typeof within>) {
  const selector = scope.getByRole('combobox', { name: text('apiReference.exampleName') });
  await userEvent.click(selector.closest('.ant-select')!);
  const next = [...document.querySelectorAll<HTMLElement>('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option')].find(option => !option.classList.contains('ant-select-item-option-selected'))!;
  await waitFor(() => expect(next).toBeVisible());
  await userEvent.click(next);
  await waitFor(() => expect(selector).toHaveAttribute('aria-expanded', 'false'));
  await waitFor(() => {
    const listbox = within(document.body).queryByRole('listbox');
    if (listbox) expect(listbox).not.toBeVisible();
  }, { timeout: 2500 });
}

const wideReference = { viewport: { defaultViewport: 'wideReference', viewports: { wideReference: { name: 'Wide reference', styles: { width: '1600px', height: '1000px' }, type: 'desktop' } } } };

export const LoginReference: Story = {
  parameters: wideReference,
  play: async ({ canvasElement }) => {
    const { row, responses } = await expandedReference(canvasElement, 'POST /login');
    const request = within(row).getByRole('group', { name: text('apiExplorer.requestBody') });
    const req = within(request), res = within(responses);
    expect(req.getByRole('tab', { name: text('apiExplorer.example') })).toHaveAttribute('aria-selected', 'true');
    const example = req.getByRole('group', { name: text('apiExplorer.example') });
    expect(JSON.parse(example.textContent!)).toHaveProperty('username');
    expect(request.querySelectorAll('.api-code-token-key').length).toBeGreaterThan(0);
    expect(request.querySelector('.api-schema-fields')).toBeNull();
    await userEvent.click(req.getByRole('tab', { name: text('apiReference.schema') }));
    const schema = JSON.parse(req.getByRole('group', { name: text('apiReference.schema') }).textContent!);
    expect(schema).toHaveProperty('properties.username');
    await userEvent.click(req.getByRole('tab', { name: text('apiReference.fields') }));
    expect(request.querySelectorAll('.api-schema-fields .api-reference-field').length).toBeGreaterThan(0);
    await userEvent.click(req.getByRole('tab', { name: text('apiExplorer.example') }));
    expect(res.getByRole('tab', { name: '200' })).toHaveAttribute('aria-selected', 'true');
    const before = res.getByRole('group', { name: text('apiExplorer.example') }).textContent;
    await nextReferenceExample(res);
    const selected = res.getByRole('group', { name: text('apiExplorer.example') });
    expect(selected.textContent).not.toBe(before);
    await userEvent.click(res.getByRole('button', { name: text('apiExplorer.copyCode') }));
    await waitFor(() => expect(ClipboardManager.copyText).toHaveBeenCalledWith(selected.textContent));
    await within(document.body).findByText(text('apiExplorer.copied'));
    assertNoOverflow(canvasElement);
  },
};

export const LoginReferenceDark: Story = { ...LoginReference, globals: { theme: 'dark' } };
export const LoginReferenceMobile: Story = { ...LoginReference, parameters: NarrowCatalogue.parameters };

export const GetReference: Story = {
  parameters: wideReference,
  play: async ({ canvasElement }) => {
    const { row, responses } = await expandedReference(canvasElement, 'GET /panel/api/clients/list/paged');
    const parameters = within(row).getByRole('group', { name: text('apiExplorer.parameters') });
    expect(parameters.querySelectorAll('.api-reference-field').length).toBeGreaterThan(0);
    const res = within(responses);
    expect(res.getByRole('tab', { name: '200' })).toHaveAttribute('aria-selected', 'true');
    expect(res.getByRole('tab', { name: text('apiExplorer.example') })).toHaveAttribute('aria-selected', 'true');
    expect(JSON.parse(res.getByRole('group', { name: text('apiExplorer.example') }).textContent!)).toHaveProperty('success');
    await userEvent.click(res.getByRole('tab', { name: text('apiReference.schema') }));
    expect(JSON.parse(res.getByRole('group', { name: text('apiReference.schema') }).textContent!)).toHaveProperty('properties');
    await userEvent.click(res.getByRole('tab', { name: text('apiReference.fields') }));
    expect(responses.querySelectorAll('.api-schema-fields .api-reference-field').length).toBeGreaterThan(0);
    await userEvent.click(res.getByRole('tab', { name: text('apiExplorer.example') }));
    assertNoOverflow(canvasElement);
  },
};

export const GetReferenceDark: Story = { ...GetReference, globals: { theme: 'dark' } };
export const GetReferenceMobile: Story = { ...GetReference, parameters: NarrowCatalogue.parameters };
