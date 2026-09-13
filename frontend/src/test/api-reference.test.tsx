import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import i18next from 'i18next';
import { ApiReference } from '@/pages/api-docs/ApiReference';
import { buildApiCatalogue, OpenApiDocumentSchema } from '@/pages/api-docs/api-document';
import { renderWithProviders } from './test-utils';

const text = (key: string) => String(i18next.t(key));
const externalUrl = 'https://example.invalid/never-fetch-this.json';
const schema = {
  type: 'object', required: ['enabled'], additionalProperties: false,
  properties: {
    enabled: { type: 'boolean', description: 'Human enable-field description' },
    names: { type: 'array', items: { type: 'string', enum: ['alpha', 'beta'] } },
    note: { type: 'string', nullable: true },
  },
};
const firstExample = { enabled: false, names: ['alpha'], note: null };
const secondExample = { enabled: true, names: ['beta'], note: 'Second inline value' };
const defaultContent = {
  'application/json': {
    schema: { $ref: '#/components/schemas/Config' },
    examples: {
      remote: { summary: 'Remote example', externalValue: externalUrl },
      first: { summary: 'First inline example', value: firstExample },
      second: { summary: 'Second inline example', value: secondExample },
    },
  },
  'text/plain': { schema: { type: 'string', minLength: 1 }, example: 'plain request fixture' },
};

function setup(content: Record<string, unknown> = defaultContent) {
  const document = OpenApiDocumentSchema.parse({
    openapi: '3.0.3', info: { title: 'Reference fixture', version: '1' },
    components: { schemas: { Config: schema } },
    paths: {
      '/reference/{id}': {
        parameters: [{ name: 'id', in: 'path', required: true, description: 'Stable resource identity', schema: { type: 'integer' } }],
        post: {
          summary: 'Reference fixture', parameters: [{ name: 'limit', in: 'query', description: 'Page limit', schema: { type: 'integer' } }],
          requestBody: { required: true, content },
          responses: {
            '200': {
              description: 'Successful response', headers: { 'X-Cursor': { description: 'Next page cursor', schema: { type: 'string' } } },
              content: {
                'application/json': { schema: { $ref: '#/components/schemas/Config' }, example: { enabled: true, names: [], note: 'Server response' } },
                'text/plain': { schema: { type: 'string' }, example: 'plain response fixture' },
              },
            },
            '204': { description: 'Empty response' },
            '404': { description: 'Missing resource', content: { 'application/problem+json': { schema: { type: 'object', properties: { error: { type: 'string' } } }, example: { error: 'Missing fixture' } } } },
            default: { description: 'Fallback response' },
          },
        },
      },
    },
  });
  const original = JSON.stringify(document);
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const entry = buildApiCatalogue(document, [])[0];
  const view = renderWithProviders(<ApiReference entry={entry} document={document} />);
  return { ...view, document, original, fetch };
}

function request() { return screen.getByRole('group', { name: text('apiExplorer.requestBody') }); }
function responses() { return screen.getByRole('group', { name: text('apiExplorer.documentedResponses') }); }
function code(section: HTMLElement, label: string) { return within(section).getByRole('group', { name: label }).textContent ?? ''; }
function tab(section: HTMLElement, label: string) { fireEvent.click(within(section).getByRole('tab', { name: label })); }
async function selectNext(section: HTMLElement, label: string, optionText: string) {
  const input = within(section).getByRole('combobox', { name: label });
  fireEvent.mouseDown(input.closest('.ant-select')!);
  fireEvent.keyDown(input, { key: 'ArrowDown', code: 'ArrowDown', keyCode: 40, which: 40 });
  fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', keyCode: 13, which: 13 });
  await waitFor(() => expect(input.closest('.ant-select')?.textContent).toContain(optionText));
}

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('code-focused API reference', () => {
  it('starts with the first inline example and selects named examples independently from the response', async () => {
    const view = setup();
    const req = request(), res = responses();
    expect(within(req).getByRole('tab', { name: text('apiExplorer.example') }).getAttribute('aria-selected')).toBe('true');
    expect(JSON.parse(code(req, text('apiExplorer.example')))).toEqual(firstExample);
    expect(within(req).queryByText('Human enable-field description')).toBeNull();
    expect(within(req).queryByText(externalUrl)).toBeNull();
    await selectNext(req, text('apiReference.exampleName'), 'Second inline example');
    expect(JSON.parse(code(req, text('apiExplorer.example')))).toEqual(secondExample);
    expect(JSON.parse(code(res, text('apiExplorer.example')))).toMatchObject({ note: 'Server response' });
    expect(JSON.stringify(view.document)).toBe(view.original);
    expect(view.fetch).not.toHaveBeenCalled();
  });

  it.each([false, 0, null, '', '{"looks":"json"}'])('treats %j as a real inline JSON value rather than missing or pre-serialized data', value => {
    setup({ 'application/json': { schema: { type: 'string' }, example: value } });
    const req = request();
    expect(within(req).getByRole('tab', { name: text('apiExplorer.example') }).getAttribute('aria-selected')).toBe('true');
    expect(code(req, text('apiExplorer.example'))).toBe(JSON.stringify(value));
    expect(within(req).queryByText(text('apiReference.noExample'))).toBeNull();
  });

  it('shows the real JSON schema and keeps human field descriptions behind their own tab', async () => {
    setup();
    const req = request();
    tab(req, text('apiReference.schema'));
    expect(JSON.parse(code(req, text('apiReference.schema')))).toEqual(schema);
    await waitFor(() => expect(within(req).queryByRole('group', { name: text('apiExplorer.example') })).toBeNull());
    tab(req, text('apiReference.fields'));
    expect(within(req).getByText('Human enable-field description')).toBeTruthy();
    expect(within(req).getByText('$.enabled', { selector: 'code' })).toBeTruthy();
    await waitFor(() => expect(within(req).queryByRole('group', { name: text('apiReference.schema') })).toBeNull());
    const parameters = screen.getByRole('group', { name: text('apiExplorer.parameters') });
    expect(within(parameters).getByText('Stable resource identity')).toBeTruthy();
    expect(within(parameters).getByText('Page limit')).toBeTruthy();
  });

  it('defaults to Schema without an inline example and does not fabricate one from defaults or enums', () => {
    setup({ 'application/json': { schema: { type: 'object', properties: { enabled: { type: 'boolean', default: true }, mode: { type: 'string', enum: ['a', 'b'] } } } } });
    const req = request();
    expect(within(req).getByRole('tab', { name: text('apiReference.schema') }).getAttribute('aria-selected')).toBe('true');
    expect(JSON.parse(code(req, text('apiReference.schema')))).toHaveProperty('properties.enabled.default', true);
    tab(req, text('apiExplorer.example'));
    expect(within(req).getByText(text('apiReference.noExample'))).toBeTruthy();
    expect(within(req).queryByRole('group', { name: text('apiExplorer.example') })).toBeNull();
  });

  it('renders boolean false schemas as JSON rather than hiding them', () => {
    setup({ 'application/json': { schema: false } });
    expect(code(request(), text('apiReference.schema'))).toBe('false');
    expect(within(request()).getByRole('tab', { name: text('apiReference.schema') }).getAttribute('aria-selected')).toBe('true');
  });

  it('switches one media type at a time without leaking another example, schema or selected name', async () => {
    setup();
    const req = request(), res = responses();
    await selectNext(req, text('apiReference.exampleName'), 'Second inline example');
    await selectNext(req, text('apiRepair.request.contentType'), 'text/plain');
    expect(code(req, text('apiExplorer.example'))).toBe('plain request fixture');
    expect(within(req).queryByRole('combobox', { name: text('apiReference.exampleName') })).toBeNull();
    tab(req, text('apiReference.schema'));
    expect(JSON.parse(code(req, text('apiReference.schema')))).toEqual({ type: 'string', minLength: 1 });
    await selectNext(res, text('apiRepair.request.contentType'), 'text/plain');
    expect(code(res, text('apiExplorer.example'))).toBe('plain response fixture');
    await selectNext(req, text('apiRepair.request.contentType'), 'application/json');
    expect(JSON.parse(code(req, text('apiExplorer.example')))).toEqual(firstExample);
    expect(code(res, text('apiExplorer.example'))).toBe('plain response fixture');
    await waitFor(() => expect(within(req).queryByRole('group', { name: text('apiReference.schema') })).toBeNull());
  });

  it('shows external-only references honestly and never fetches their URLs', () => {
    const view = setup({ 'application/json': { schema, examples: { remote: { externalValue: externalUrl } } } });
    const req = request();
    expect(within(req).getByRole('tab', { name: text('apiReference.schema') }).getAttribute('aria-selected')).toBe('true');
    tab(req, text('apiExplorer.example'));
    expect(within(req).getByText(externalUrl)).toBeTruthy();
    expect(within(req).getByText(text('apiReference.externalExample'))).toBeTruthy();
    expect(within(req).queryByRole('group', { name: text('apiExplorer.example') })).toBeNull();
    expect(view.fetch).not.toHaveBeenCalled();
  });

  it('switches response statuses, discloses headers and clears the body for empty responses', async () => {
    const view = setup();
    const res = responses();
    expect(within(res).getByRole('tab', { name: '200' }).getAttribute('aria-selected')).toBe('true');
    const headers = res.querySelector<HTMLDetailsElement>('.api-reference-headers')!;
    expect(headers.open).toBe(false);
    fireEvent.click(headers.querySelector('summary')!);
    expect(headers.open).toBe(true);
    expect(within(headers).getByText('X-Cursor')).toBeTruthy();
    tab(res, '404');
    expect(JSON.parse(code(res, text('apiExplorer.example')))).toEqual({ error: 'Missing fixture' });
    await waitFor(() => expect(within(res).queryByText('Next page cursor')).toBeNull());
    expect(JSON.parse(code(request(), text('apiExplorer.example')))).toEqual(firstExample);
    tab(res, '204');
    expect(within(res).getByText(text('apiReference.noBody'))).toBeTruthy();
    expect(within(res).queryByRole('group', { name: text('apiExplorer.example') })).toBeNull();
    tab(res, 'default');
    expect(within(res).getByText('Fallback response')).toBeTruthy();
    expect(within(res).getByText(text('apiReference.noBody'))).toBeTruthy();
    expect(JSON.stringify(view.document)).toBe(view.original);
    expect(view.fetch).not.toHaveBeenCalled();
  });

  it('distinguishes an undocumented binary sample from a response without a body', () => {
    setup({ 'application/octet-stream': { schema: { type: 'string', format: 'binary' } } });
    const req = request();
    expect(JSON.parse(code(req, text('apiReference.schema')))).toEqual({ type: 'string', format: 'binary' });
    tab(req, text('apiExplorer.example'));
    expect(within(req).getByText(text('apiReference.binaryExample'))).toBeTruthy();
    expect(within(req).queryByText(text('apiReference.noBody'))).toBeNull();
  });
});
