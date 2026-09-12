import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { HttpUtil, Msg } from '@/utils';
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
    const fetch = spyOn(globalThis, 'fetch').mockImplementation(async input => {
      if (!String(input).endsWith('/panel/api/openapi.json')) throw new Error(`Story attempted request execution: ${String(input)}`);
      if (failRead) { failRead = false; return new Response('Unavailable', { status: 503, statusText: 'Unavailable' }); }
      return new Response(JSON.stringify(storyDocument(user)), { headers: { 'Content-Type': 'application/json' } });
    });
    return () => { get.mockRestore(); post.mockRestore(); fetch.mockRestore(); };
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
    expect(canvas.getByRole('combobox', { name: text('apiExplorer.categories') }).closest('.ant-select')).toBeVisible();
    expect(canvas.queryByRole('navigation', { name: text('apiExplorer.categories') })).toBeNull();
    expect(canvas.getByRole('button', { name: new RegExp(text('apiRepair.request.send')) })).toBeVisible();
    expect(canvas.getAllByText(text('apiRepair.request.unsentHint')).length).toBeGreaterThan(0);
    const firstOperation = canvasElement.querySelector<HTMLElement>('.p-eprow')!;
    expect(firstOperation.getBoundingClientRect().top).toBeLessThan(window.innerHeight);
    for (const key of ['liveResponse', 'responseHeaders', 'responseExample']) expect(canvas.getByRole('tab', { name: text(`apiRepair.request.${key}`) })).toBeVisible();
    await userEvent.click(canvas.getByRole('tab', { name: text('apiRepair.request.responseExample') }));
    await waitFor(() => expect(canvas.getByText(text('apiRepair.request.exampleOnly'))).toBeVisible());
    await userEvent.click(canvas.getByRole('tab', { name: text('apiRepair.request.liveResponse') }));
    assertNoOverflow(canvasElement);
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
