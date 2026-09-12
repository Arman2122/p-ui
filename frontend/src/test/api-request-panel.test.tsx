import { StrictMode } from 'react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import ApiRequestPanel from '@/pages/api-docs/ApiRequestPanel';
import { OpenApiDocumentSchema } from '@/pages/api-docs/api-document';
import type { ApiRequestAuth } from '@/pages/api-docs/request-transport';
import { ClipboardManager } from '@/utils';
import { renderWithProviders } from './test-utils';

const label = (key: string) => i18next.t(`apiRepair.request.${key}`);
const endpoint = { method: 'GET' as const, path: '/panel/api/items/:id', summary: 'Look up <code>id</code> safely.' };
const operation = {
  parameters: [{ name: 'id', in: 'path', required: true, schema: { type: 'integer' }, description: 'The <code>id</code> field.' }],
  responses: { '200': { description: 'OK', content: { 'application/json': { example: { success: true, obj: { exampleOnly: 'example-data' } } } } } },
};
function spec(method: 'get' | 'post' = 'get', extras: Record<string, unknown> = {}, path = '/panel/api/items/{id}') {
  return OpenApiDocumentSchema.parse({ openapi: '3.0.3', info: { title: 'Test', version: '1' }, paths: { [path]: { [method]: { ...operation, ...extras } } } });
}
function sendButton() { return screen.getByRole('button', { name: label('send') }) as HTMLButtonElement; }
function editId(id = '7') { fireEvent.change(screen.getByRole('textbox', { name: 'path: id' }), { target: { value: id } }); }
beforeEach(() => { window.PUI_BASE_PATH = '/secret/'; });
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); delete window.PUI_BASE_PATH; });

describe('native API request panel', () => {
  it('enables a no-parameter GET when the parent passes an empty root base', () => {
    window.PUI_BASE_PATH = '/';
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<ApiRequestPanel endpoint={{ method: 'GET', path: '/panel/api/me', summary: 'Account' }} document={spec('get', { parameters: [] }, '/panel/api/me')} auth={{ mode: 'session' }} basePath="" />);
    expect(sendButton().disabled).toBe(false);
    expect(document.querySelector('.p-apirequest-url')?.textContent).toBe(`${window.location.origin}/panel/api/me`);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('never auto-sends, disables unresolved or malformed paths, renders documentation markup, and separates examples', async () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<StrictMode><ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'session' }} /></StrictMode>);
    expect(fetcher).not.toHaveBeenCalled(); expect(sendButton().disabled).toBe(true);
    expect(document.body.textContent).not.toContain('<code>'); expect(document.querySelector('.p-apirequest-description code')?.textContent).toBe('id');
    editId('bad'); expect(sendButton().disabled).toBe(true);
    editId(); expect(sendButton().disabled).toBe(false);
    fireEvent.click(screen.getByRole('tab', { name: label('responseExample') }));
    await screen.findByText(/example-data/);
    expect(fetcher).not.toHaveBeenCalled(); expect(screen.getByText(label('unsent'))).not.toBeNull();
  });

  it('sends only on click under the panel prefix and displays HTTP errors as raw text', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('real denied body', { status: 403, statusText: 'Forbidden', headers: { 'content-type': 'text/plain', 'x-result': 'real-header' } })); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<StrictMode><ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'session' }} /></StrictMode>);
    editId(); fireEvent.click(sendButton());
    await screen.findByText('real denied body');
    expect(fetcher).toHaveBeenCalledTimes(1); expect(fetcher.mock.calls[0][0]).toBe(`${window.location.origin}/secret/panel/api/items/7`);
    expect(screen.getByText('403 Forbidden')).not.toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: label('responseHeaders') }));
    await screen.findByText(/x-result: real-header/);
  });

  it('shows an HTTP200 success:false warning and clears it when the parameter changes', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"success":false,"msg":"server refused"}', { headers: { 'content-type': 'application/json' } })));
    renderWithProviders(<ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'session' }} />);
    editId(); fireEvent.click(sendButton()); await screen.findByText(label('envelopeFailed'));
    expect(document.querySelector('.ant-alert-warning')).not.toBeNull();
    editId('8'); expect(screen.queryByText(label('envelopeFailed'))).toBeNull(); expect(screen.getByText(label('unsent'))).not.toBeNull();
  });

  it('does not describe an HTTP401 envelope as an HTTP success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"success":false,"msg":"Unauthorized"}', { status: 401, statusText: 'Unauthorized', headers: { 'content-type': 'application/json' } })));
    renderWithProviders(<ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'bearer', token: 'invalid' }} />);
    editId(); fireEvent.click(sendButton());
    await screen.findByText('401 Unauthorized');
    expect(screen.queryByText(label('envelopeFailed'))).toBeNull();
    expect(screen.getByRole('tabpanel', { name: label('liveResponse') }).textContent).toContain('"msg": "Unauthorized"');
  });

  it('requires valid JSON and obtains CSRF only after explicit Send', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(new Response('{"success":true,"obj":"csrf-value"}')).mockResolvedValueOnce(new Response('{"success":true,"obj":"saved"}', { headers: { 'content-type': 'application/json' } })); vi.stubGlobal('fetch', fetcher);
    const doc = spec('post', { requestBody: { required: true, content: { 'application/json': { schema: { type: 'object', required: ['active'], properties: { active: { type: 'boolean' } } } } } } });
    renderWithProviders(<ApiRequestPanel endpoint={{ ...endpoint, method: 'POST' }} document={doc} auth={{ mode: 'session' }} />);
    editId(); fireEvent.change(screen.getByRole('textbox', { name: label('jsonBody') }), { target: { value: '{broken' } });
    expect(sendButton().disabled).toBe(true); expect(fetcher).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('textbox', { name: label('jsonBody') }), { target: { value: '{"active":false}' } });
    fireEvent.click(sendButton()); await screen.findByText(/"saved"/);
    expect(fetcher).toHaveBeenCalledTimes(2); expect((fetcher.mock.calls[1][1].headers as Headers).get('X-CSRF-Token')).toBe('csrf-value');
    expect(fetcher.mock.calls[1][1].body).toBe('{"active":false}');
  });

  it('does not submit the operation after a CSRF failure', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('no CSRF', { status: 403 })); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<ApiRequestPanel endpoint={{ ...endpoint, method: 'POST' }} document={spec('post')} auth={{ mode: 'session' }} />);
    editId(); fireEvent.click(sendButton()); await screen.findByText(new RegExp(label('csrfFailed').replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('cancels an outstanding request and ignores a late response', async () => {
    let finish!: (response: Response) => void;
    const fetcher = vi.fn().mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve; })); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'session' }} />);
    editId(); fireEvent.click(sendButton());
    fireEvent.click(await screen.findByRole('button', { name: label('cancel') }));
    expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
    await act(async () => { finish(new Response('obsolete response')); });
    expect(screen.queryByText('obsolete response')).toBeNull(); expect(screen.getByText(label('cancelled'))).not.toBeNull();
  });

  it.each(['token', 'identity', 'endpoint'] as const)('aborts and clears previous state when %s changes', async (change) => {
    const storage = vi.spyOn(Storage.prototype, 'setItem');
    let finish!: (response: Response) => void;
    const fetcher = vi.fn().mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve; })); vi.stubGlobal('fetch', fetcher);
    const doc = spec(), auth: ApiRequestAuth = { mode: 'bearer', token: 'first-token', identityKey: 'user-one' };
    const view = renderWithProviders(<ApiRequestPanel endpoint={endpoint} document={doc} auth={auth} />);
    editId(); fireEvent.click(sendButton());
    const next = change === 'token' ? { ...auth, token: 'second-token' } : { ...auth, identityKey: 'user-two' };
    view.rerender(<ApiRequestPanel endpoint={change === 'endpoint' ? { ...endpoint, path: '/panel/api/other/:id' } : endpoint} document={doc} auth={next} />);
    expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
    await act(async () => { finish(new Response('previous-user-data')); });
    expect(screen.queryByText('previous-user-data')).toBeNull(); expect(screen.getByText(label('unsent'))).not.toBeNull();
    expect(storage.mock.calls.some((args) => args.some((value) => value.includes('first-token') || value.includes('second-token')))).toBe(false);
  });

  it('keeps destructive requests behind an explicit confirmation and does not send on cancel', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('{}', { headers: { 'content-type': 'application/json' } })); vi.stubGlobal('fetch', fetcher);
    const path = '/panel/api/items/{id}/delete';
    renderWithProviders(<ApiRequestPanel endpoint={{ ...endpoint, method: 'POST', path: '/panel/api/items/:id/delete' }} document={spec('post', {}, path)} auth={{ mode: 'bearer', token: 'token' }} />);
    editId(); fireEvent.click(sendButton());
    await screen.findByRole('dialog'); expect(fetcher).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: i18next.t('cancel') }));
    await waitFor(() => expect(sendButton().disabled).toBe(false));
    expect(fetcher).not.toHaveBeenCalled();
  });

  it('sends the reviewed destructive request once after confirmation', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response('{}', { headers: { 'content-type': 'application/json' } })); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<ApiRequestPanel endpoint={{ ...endpoint, method: 'POST', path: '/panel/api/items/:id/delete' }} document={spec('post', {}, '/panel/api/items/{id}/delete')} auth={{ mode: 'bearer', token: 'token' }} />);
    editId();
    fireEvent.click(sendButton());
    fireEvent.click(await screen.findByRole('button', { name: label('confirmSend') }));
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  });

  it('copies the generated request and response without fetching again', async () => {
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    const fetcher = vi.fn().mockResolvedValue(new Response('raw reply', { headers: { 'content-type': 'text/plain' } })); vi.stubGlobal('fetch', fetcher);
    renderWithProviders(<ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'session' }} />);
    editId(); fireEvent.click(screen.getByRole('button', { name: label('copySnippet') }));
    expect(copy.mock.calls[0][0]).toContain('curl --request'); expect(fetcher).not.toHaveBeenCalled();
    fireEvent.click(sendButton()); await screen.findByText('raw reply');
    fireEvent.click(screen.getByRole('button', { name: label('copyResponse') }));
    expect(copy).toHaveBeenCalledWith('raw reply'); expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('formats JSON by default without changing large literals and copies the original raw bytes', async () => {
    const raw = '{"count":18446744073709551615,"items":[1,2]}';
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(raw, { headers: { 'content-type': 'application/json' } })));
    renderWithProviders(<ApiRequestPanel endpoint={endpoint} document={spec()} auth={{ mode: 'session' }} />);
    editId(); fireEvent.click(sendButton());
    await screen.findByRole('radio', { name: label('rawJson') });
    const live = screen.getByRole('tabpanel', { name: label('liveResponse') }).querySelector('.p-code');
    expect(live?.textContent).toContain('\n  "count": 18446744073709551615,\n');
    fireEvent.click(screen.getByRole('button', { name: label('copyResponse') }));
    expect(copy).toHaveBeenCalledWith(raw);
    fireEvent.click(screen.getByRole('radio', { name: label('rawJson') }));
    expect(live?.textContent).toBe(raw);
  });
});
