import { StrictMode, type ReactElement, type ReactNode } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import i18next from 'i18next';
import ApiRequestPanel, { apiRequestDraftKey } from '@/pages/api-docs/ApiRequestPanel';
import { OpenApiDocumentSchema } from '@/pages/api-docs/api-document';
import type { Endpoint } from '@/pages/api-docs/endpoints';
import type { ApiRequestDraft } from '@/pages/api-docs/request-inputs';
import { QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from '@/hooks/useTheme';
import { makeTestQueryClient } from './test-utils';

function renderPanel(ui: ReactElement) {
  const client = makeTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}><ThemeProvider>{children}</ThemeProvider></QueryClientProvider>;
  return render(ui, { wrapper });
}

const text = (key: string) => i18next.t(key);
const login: Endpoint = { method: 'POST', path: '/login', summary: 'Authenticate.' };
const simple = { type: 'object', required: ['username', 'password'], properties: {
  username: { type: 'string' }, password: { type: 'string', format: 'password' }, twoFactorCode: { type: 'string' }, enabled: { type: 'boolean' }, count: { type: 'integer' }, comment: { type: ['string', 'null'] },
} };
const doc = OpenApiDocumentSchema.parse({ openapi: '3.0.3', info: { title: 'Test', version: '1' }, paths: {
  '/login': { post: { parameters: [{ in: 'header', name: 'X-CSRF-Token', required: true, schema: { type: 'string' } }], requestBody: { required: true, content: { 'application/json': { schema: simple } } } } },
  '/panel/api/items/{id}': { get: { parameters: [{ in: 'path', name: 'id', required: true, schema: { type: 'integer' } }, { in: 'query', name: 'search', schema: { type: 'string' } }] } },
  '/panel/api/upload': { post: { requestBody: { content: { 'multipart/form-data': { schema: { type: 'object', properties: { file: { type: 'string', format: 'binary' } } } } } } } },
} });
const auth = { mode: 'session' as const, identityKey: 'user:1' };
const send = () => screen.getByRole('button', { name: text('apiRepair.request.send') });
const body = () => screen.getByRole('textbox', { name: text('apiRepair.request.jsonBody') }) as HTMLTextAreaElement;
const input = (name: string) => screen.getByLabelText(`body: ${name}`) as HTMLInputElement;
function fill(name: string, value: string) { fireEvent.change(input(name), { target: { value } }); }

beforeEach(() => { window.PUI_BASE_PATH = '/'; });
afterEach(() => { delete window.PUI_BASE_PATH; vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('request workspace UX', () => {
  it('isolates guided API credentials from login autofill while preserving vertical labels and keyboard typing', async () => {
    const user = userEvent.setup();
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    renderPanel(<ApiRequestPanel endpoint={login} document={doc} auth={auth} />);
    const username = input('username'), password = input('password');
    expect(username.getAttribute('autocomplete')).toBe('off');
    expect(password.getAttribute('autocomplete')).toBe('new-password');
    for (const control of [username, password]) {
      expect(control.name).toMatch(/^api-request-/);
      expect(control.getAttribute('data-lpignore')).toBe('true');
      expect(control.getAttribute('data-1p-ignore')).toBe('true');
      expect(control.labels?.length).toBe(1);
      expect(control.labels?.[0].htmlFor).toBe(control.id);
      expect(control.closest('.ant-form-vertical')).not.toBeNull();
      expect(control.value).toBe('');
    }
    await user.click(username.labels![0]);
    expect(document.activeElement).toBe(username);
    await user.keyboard('typed-api-user');
    await user.tab();
    expect(document.activeElement).toBe(password);
    await user.keyboard('typed-api-password');
    await user.click(screen.getByRole('radio', { name: 'JSON' }).closest('label')!);
    expect(JSON.parse(body().value)).toEqual({ username: 'typed-api-user', password: 'typed-api-password' });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('shows concise auth/language help and file guidance only for selected multipart files', async () => {
    const user = userEvent.setup();
    const items: Endpoint = { method: 'GET', path: '/panel/api/items/:id', summary: 'Read' };
    const upload: Endpoint = { method: 'POST', path: '/panel/api/upload', summary: 'Upload' };
    const view = renderPanel(<ApiRequestPanel endpoint={items} document={doc} auth={auth} />);
    await user.click(screen.getByRole('tab', { name: text('apiUx.code') }));
    expect(screen.getByText(text('apiUx.snippetScriptSession'))).not.toBeNull();
    expect(screen.queryByText(text('apiUx.snippetFiles'), { exact: false })).toBeNull();
    await user.click(screen.getByRole('combobox', { name: text('apiRepair.request.language') }).closest('.ant-select')!);
    await user.click(Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option')).find((item) => item.getAttribute('title') === 'JavaScript')!);
    expect(screen.getByText(text('apiUx.snippetBrowserSession'))).not.toBeNull();
    view.rerender(<ApiRequestPanel endpoint={items} document={doc} auth={{ ...auth, mode: 'bearer', token: 't' }} />);
    expect(screen.getByText(text('apiUx.snippetBearer'))).not.toBeNull();
    view.rerender(<ApiRequestPanel endpoint={upload} document={doc} auth={auth} />);
    fireEvent.change(screen.getByLabelText('body: file'), { target: { files: [new File(['data'], 'file.bin')] } });
    await user.click(screen.getByRole('tab', { name: text('apiUx.code') }));
    expect(screen.getByText(text('apiUx.snippetFiles'), { exact: false })).not.toBeNull();
  });

  it('lets an explicit request complete after draft edits and labels its captured original query URL', async () => {
    let finish!: (response: Response) => void;
    const fetcher = vi.fn().mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve; }));
    vi.stubGlobal('fetch', fetcher);
    const endpoint: Endpoint = { method: 'GET', path: '/panel/api/items/:id', summary: 'Read' };
    renderPanel(<ApiRequestPanel endpoint={endpoint} document={doc} auth={auth} />);
    const id = screen.getByRole('textbox', { name: 'path: id' });
    const search = screen.getByRole('textbox', { name: 'query: search' });
    fireEvent.change(id, { target: { value: '7' } });
    fireEvent.change(search, { target: { value: 'original query' } });
    fireEvent.click(send());
    expect(fetcher).toHaveBeenCalledTimes(1);
    fireEvent.change(id, { target: { value: '8' } });
    fireEvent.change(search, { target: { value: 'edited query' } });
    expect(fetcher.mock.calls[0][1].signal.aborted).toBe(false);
    expect(screen.getByRole('button', { name: text('apiRepair.request.cancel') })).not.toBeNull();
    await act(async () => { finish(new Response('{"success":true,"obj":"original result"}', { headers: { 'content-type': 'application/json' } })); });
    await screen.findByText(/"original result"/);
    expect(screen.getByText(text('apiUx.previousRequest'))).not.toBeNull();
    expect(document.querySelector('.p-apirequest-evidence')?.textContent).toContain('GET http://localhost:3000/panel/api/items/7?search=original+query');
    expect(document.querySelector('.p-apirequest-url')?.textContent).toContain('/items/8?search=edited+query');
    fireEvent.change(search, { target: { value: 'another edit' } });
    expect(screen.getByText(/"original result"/)).not.toBeNull();
    fireEvent.click(send());
    expect(screen.queryByText(/"original result"/)).toBeNull();
    expect(document.querySelector('.p-apirequest-evidence')?.textContent).toContain('/items/8?search=another+edit');
    fireEvent.click(screen.getByRole('button', { name: text('apiRepair.request.cancel') }));
  });

  it('shows an explicit object when switching empty fields to JSON and never sends hidden JSON after clearing it', () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    renderPanel(<ApiRequestPanel endpoint={login} document={doc} auth={auth} />);
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }));
    expect(body().value).toBe('{}');
    fireEvent.change(body(), { target: { value: '' } });
    fireEvent.click(send());
    expect(fetcher).not.toHaveBeenCalled();
    expect(body().value).toBe('');
    expect(document.activeElement).toBe(body());
    expect(screen.getByText(text('apiRepair.request.required'))).not.toBeNull();
  });

  it('types into real guided controls and round-trips false, zero, null and unknown precision', async () => {
    const user = userEvent.setup();
    renderPanel(<ApiRequestPanel endpoint={login} document={doc} auth={auth} />);
    await user.click(screen.getByRole('radio', { name: 'JSON' }).closest('label')!);
    fireEvent.change(body(), { target: { value: '{"extra":{"id":18446744073709551615,"fraction":1.2300e+40}}' } });
    await user.click(screen.getByRole('radio', { name: text('apiUx.fields') }).closest('label')!);
    expect(screen.getByRole('button', { name: text('apiUx.openJson') })).not.toBeNull();
    await user.type(input('username'), 'operator');
    await user.type(input('password'), 'typed-password');
    await user.type(input('count'), '0');
    async function choose(name: string, option: string) {
      await user.click(screen.getByRole('combobox', { name }).closest('.ant-select')!);
      const target = Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option')).find((item) => item.getAttribute('title') === option);
      expect(target).not.toBeUndefined();
      await user.click(target!);
    }
    await choose('body: enabled', 'false');
    await choose(i18next.t('apiUx.presence', { name: 'comment' }), 'null');
    await user.click(screen.getByRole('button', { name: text('apiUx.openJson') }));
    expect(body().value).toContain('18446744073709551615');
    expect(body().value).toContain('1.2300e+40');
    expect(JSON.parse(body().value)).toMatchObject({ username: 'operator', password: 'typed-password', count: 0, enabled: false, comment: null });
    expect(Object.hasOwn(JSON.parse(body().value), 'twoFactorCode')).toBe(false);
    await user.click(screen.getByRole('radio', { name: text('apiUx.fields') }).closest('label')!);
    expect(input('username').value).toBe('operator');
    expect(input('count').value).toBe('0');
    expect(document.querySelector('label label')).toBeNull();
  });

  it('starts login with empty primitive fields, no automated header card or premature errors, and focuses the missing field on Send', async () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    renderPanel(<StrictMode><ApiRequestPanel endpoint={login} document={doc} auth={auth} /></StrictMode>);
    expect(input('username').value).toBe(''); expect(input('password').value).toBe('');
    expect(screen.queryByRole('tab', { name: text('apiRepair.request.parameters') })).toBeNull();
    expect(screen.queryByLabelText('header: X-CSRF-Token')).toBeNull();
    expect(document.querySelector('.ant-form-item-explain-error')).toBeNull();
    expect(fetcher).not.toHaveBeenCalled();
    fireEvent.click(send());
    await waitFor(() => expect(document.activeElement).toBe(input('username')));
    expect(document.querySelector('.ant-form-item-explain-error')).not.toBeNull();
    expect(fetcher).not.toHaveBeenCalled();
    expect(document.querySelector('.p-apirequest-workspace > .p-apirequest-editor')).not.toBeNull();
    expect(document.querySelector('.p-apirequest-workspace > .p-apirequest-response')).not.toBeNull();
  });

  it('preserves false, zero, null, unknown properties and precise literals across JSON and fields', () => {
    renderPanel(<ApiRequestPanel endpoint={login} document={doc} auth={auth} />);
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }));
    fireEvent.change(body(), { target: { value: '{"username":"old","password":"p","enabled":false,"count":0,"comment":null,"extra":{"id":18446744073709551615}}' } });
    fireEvent.click(screen.getByRole('radio', { name: text('apiUx.fields') }));
    expect(input('username').value).toBe('old'); expect(input('count').value).toBe('0');
    fill('username', 'new');
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }));
    expect(body().value).toContain('18446744073709551615');
    const parsed = JSON.parse(body().value);
    expect(parsed.username).toBe('new'); expect(parsed.enabled).toBe(false); expect(parsed.count).toBe(0); expect(parsed.comment).toBeNull();
    expect(Object.hasOwn(parsed, 'twoFactorCode')).toBe(false);
  });

  it('keeps invalid JSON on auth changes and cloned specification rerenders without storing auth', () => {
    const cache = new Map<string, ApiRequestDraft>();
    const view = renderPanel(<ApiRequestPanel endpoint={login} document={doc} auth={auth} draftCache={cache} />);
    fireEvent.click(screen.getByRole('radio', { name: 'JSON' }));
    fireEvent.change(body(), { target: { value: '{"unfinished":' } });
    view.rerender(<ApiRequestPanel endpoint={{ ...login }} document={{ ...doc }} auth={{ mode: 'bearer', token: 'secret-token', identityKey: auth.identityKey }} draftCache={cache} />);
    expect(body().value).toBe('{"unfinished":');
    expect((screen.getByRole('radio', { name: text('apiUx.fields') }) as HTMLInputElement).disabled).toBe(true);
    expect(JSON.stringify([...cache.values()])).not.toContain('secret-token');
  });

  it('restores per-endpoint drafts with canonical path keys and clears file selections on endpoint unmount only', () => {
    const cache = new Map<string, ApiRequestDraft>();
    const items: Endpoint = { method: 'GET', path: '/panel/api/items/:id', summary: 'Read' };
    const upload: Endpoint = { method: 'POST', path: '/panel/api/upload', summary: 'Upload' };
    const view = renderPanel(<ApiRequestPanel endpoint={items} document={doc} auth={auth} draftCache={cache} />);
    fireEvent.change(screen.getByRole('textbox', { name: 'path: id' }), { target: { value: '42' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'query: search' }), { target: { value: 'keep me' } });
    view.rerender(<ApiRequestPanel endpoint={upload} document={doc} auth={auth} draftCache={cache} />);
    const fileInput = screen.getByLabelText('body: file') as HTMLInputElement;
    const file = new File(['payload'], 'test.bin');
    fireEvent.change(fileInput, { target: { files: [file] } });
    view.rerender(<ApiRequestPanel endpoint={upload} document={doc} auth={{ mode: 'bearer', token: 't', identityKey: auth.identityKey }} draftCache={cache} />);
    expect(screen.getByLabelText('body: file')).toBe(fileInput);
    expect(fileInput.files?.[0]).toBe(file);
    expect(cache.get(apiRequestDraftKey(upload))?.files).toEqual({});
    view.rerender(<ApiRequestPanel endpoint={{ ...items, path: '/panel/api/items/{id}' }} document={doc} auth={auth} draftCache={cache} />);
    expect((screen.getByRole('textbox', { name: 'path: id' }) as HTMLInputElement).value).toBe('42');
    expect((screen.getByRole('textbox', { name: 'query: search' }) as HTMLInputElement).value).toBe('keep me');
    view.rerender(<ApiRequestPanel endpoint={upload} document={doc} auth={auth} draftCache={cache} />);
    expect((screen.getByLabelText('body: file') as HTMLInputElement).files?.length).toBe(0);
    expect(screen.queryByText('test.bin')).toBeNull();
  });

  it('blocks incomplete numeric field edits without discarding them on auth changes', async () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    const view = renderPanel(<ApiRequestPanel endpoint={login} document={doc} auth={auth} />);
    fill('username', 'user'); fill('password', 'pass'); fill('count', '-');
    expect(input('count').value).toBe('-');
    expect((screen.getByRole('radio', { name: 'JSON' }) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: text('apiUx.format') }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(send()); expect(fetcher).not.toHaveBeenCalled();
    await waitFor(() => expect(document.activeElement).toBe(input('count')));
    view.rerender(<ApiRequestPanel endpoint={login} document={doc} auth={{ ...auth, mode: 'bearer', token: 't' }} />);
    expect(input('count').value).toBe('-');
  });
});
