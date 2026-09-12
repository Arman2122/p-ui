import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import i18next from 'i18next';
import type { ComponentProps } from 'react';
import type SwaggerUI from 'swagger-ui-react';
import type { SignedInUser } from '@/pages/resellers/schema';
import ApiExplorer from '@/pages/api-docs/ApiExplorer';
import { renderWithProviders } from './test-utils';

type SwaggerUIProps = ComponentProps<typeof SwaggerUI>;

const state = vi.hoisted(() => ({ user: undefined as SignedInUser | undefined, navigate: vi.fn(), swagger: vi.fn<(props: SwaggerUIProps) => void>() }));
vi.mock('@/pages/resellers/api', () => ({ useSignedInUser: () => ({ data: state.user, isPending: !state.user, isError: false }) }));
vi.mock('react-router', async (original) => ({ ...await original<typeof import('react-router')>(), useNavigate: () => state.navigate }));
vi.mock('swagger-ui-react', () => ({ default: (props: SwaggerUIProps) => { state.swagger(props); return <div data-testid="swagger" />; } }));
vi.mock('@/pages/api-docs/endpoints', () => ({ sections: [{ title: 'Endpoints', endpoints: [
  '/panel/api/me', '/panel/api/activity', '/panel/api/activity/read', '/panel/api/activity/alerts',
  '/panel/api/reseller/clients', '/panel/api/reseller/plans', '/panel/api/reseller/missing',
  '/panel/api/resellers', '/panel/api/setting/apiTokens', '/panel/api/clients',
].map((path) => ({ path, method: 'GET', summary: path })) }] }));

function signIn(role: 'admin' | 'reseller', plans = false) {
  state.user = { id: role === 'admin' ? 1 : 2, username: role, role, home: '/', permissions: { plans, reset: false, extend: false, suspend: false, topUp: false } };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ openapi: '3.0.0',
    tags: [{ name: 'Identity', description: 'Global identity introduction' }, { name: 'Activity', description: 'Global activity introduction' }, { name: 'Administration', description: 'Global administrator introduction' }],
    security: [{ bearerAuth: [] }],
    components: { securitySchemes: { bearerAuth: { type: 'http', scheme: 'bearer' } }, schemas: { Result: { type: 'object' } } },
    paths: Object.fromEntries([
    '/panel/api/me', '/panel/api/activity', '/panel/api/activity/read', '/panel/api/activity/alerts',
    '/panel/api/reseller/clients', '/panel/api/reseller/plans', '/panel/api/resellers', '/panel/api/setting/apiTokens', '/panel/api/clients',
  ].map((path) => [path, { get: { summary: path, tags: [path === '/panel/api/me' ? 'Identity' : path.startsWith('/panel/api/activity') ? 'Activity' : 'Administration'], responses: { '200': { description: 'Result', content: { 'application/json': { schema: { $ref: '#/components/schemas/Result' } } } } } } }])) }), { status: 200 })));
}

afterEach(() => { vi.unstubAllGlobals(); state.navigate.mockClear(); state.swagger.mockClear(); state.user = undefined; });

describe('API explorer authenticated role', () => {
  it('does not reveal or fetch endpoints before the authenticated user resolves', () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const view = renderWithProviders(<ApiExplorer />);
    expect(view.container.querySelectorAll('.p-eprow')).toHaveLength(0);
    expect(fetch).not.toHaveBeenCalled();
  });

  it('limits a reseller to served permitted operations and sends token management to Account', async () => {
    signIn('reseller');
    vi.stubGlobal('PUI_ROLE', 'admin');
    const view = renderWithProviders(<ApiExplorer />);
    await waitFor(() => expect(view.container.querySelectorAll('.p-eprow')).toHaveLength(5));
    const paths = [...view.container.querySelectorAll('.p-eprow .p-mono')].map((row) => row.textContent);
    expect(paths).toEqual(['/panel/api/me', '/panel/api/activity', '/panel/api/activity/read', '/panel/api/activity/alerts', '/panel/api/reseller/clients']);
    fireEvent.click(screen.getByRole('button', { name: String(i18next.t('design.apiTokens')) }));
    expect(state.navigate).toHaveBeenCalledWith('/reseller/account');
  });

  it('keeps the administrator catalogue and global token destination', async () => {
    signIn('admin');
    const view = renderWithProviders(<ApiExplorer />);
    await waitFor(() => expect(view.container.querySelectorAll('.p-eprow')).toHaveLength(10));
    fireEvent.click(screen.getByRole('button', { name: String(i18next.t('design.apiTokens')) }));
    expect(state.navigate).toHaveBeenCalledWith('/settings#tokens');
  });

  it.each(['admin', 'reseller'] as const)('focuses %s Swagger metadata on the selected operation', async (role) => {
    signIn(role);
    renderWithProviders(<ApiExplorer />);
    await screen.findByTestId('swagger');
    const props = state.swagger.mock.lastCall?.[0];
    expect(props?.spec).toHaveProperty('tags', [{ name: 'Identity' }]);
    expect(props?.defaultModelsExpandDepth).toBe(-1);
    expect(props?.tryItOutEnabled).toBe(true);
    expect(props?.requestInterceptor).toBeTypeOf('function');
    expect(props?.spec).toHaveProperty('components.schemas.Result', { type: 'object' });
    expect(props?.spec).toHaveProperty('components.securitySchemes.bearerAuth', { type: 'http', scheme: 'bearer' });
    expect(props?.spec).toHaveProperty('security', [{ bearerAuth: [] }]);
    expect(props?.spec).toHaveProperty('servers', [{ url: `${window.location.origin}${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}` }]);
    expect(props?.spec).toHaveProperty(['paths', '/panel/api/me', 'get', 'responses', '200', 'content', 'application/json', 'schema', '$ref'], '#/components/schemas/Result');
    fireEvent.click(screen.getByRole('button', { name: 'GET /panel/api/activity' }));
    await waitFor(() => expect(state.swagger.mock.lastCall?.[0].spec).toHaveProperty('tags', [{ name: 'Activity' }]));
  });
});
