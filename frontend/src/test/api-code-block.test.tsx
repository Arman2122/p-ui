import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import { ApiCodeBlock } from '@/pages/api-docs/ApiCodeBlock';
import { ClipboardManager } from '@/utils';
import { getMessage } from '@/utils/messageBus';

const copyLabel = () => i18next.t('apiExplorer.copyCode');
afterEach(() => { vi.restoreAllMocks(); });

describe('ApiCodeBlock', () => {
  it('highlights JSON token types while preserving every character and numeric lexeme', () => {
    const stringToken = JSON.stringify('literal "quote" and \\ slash');
    const value = `  {\r\n\t"huge":18446744073709551615,"ratio":-1.23000e+42,"message":${stringToken},"ok":false,"nothing":null\r\n}\n`;
    const view = render(<ApiCodeBlock value={value} language="application/problem+json" label="Response example" />);
    const code = view.container.querySelector('pre code')!;
    expect(code.textContent).toBe(value);
    expect([...code.querySelectorAll('.api-code-token-number')].map((node) => node.textContent)).toEqual(['18446744073709551615', '-1.23000e+42']);
    expect([...code.querySelectorAll('.api-code-token-literal')].map((node) => node.textContent)).toEqual(['false', 'null']);
    expect(code.querySelector('.api-code-token-key')?.textContent).toBe('"huge"');
    expect(code.querySelector('.api-code-token-string')?.textContent).toBe(stringToken);
    expect(screen.getByText('JSON')).not.toBeNull();
    expect(screen.getByRole('group', { name: 'Response example' }).getAttribute('dir')).toBe('ltr');
  });

  it('copies the original unmodified string rather than highlighted markup or normalized JSON', async () => {
    const value = '\r\n {"id":18446744073709551615,"value":1.000e+20} \t';
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    const success = vi.spyOn(getMessage(), 'success').mockImplementation(vi.fn());
    render(<ApiCodeBlock value={value} language="json" />);
    fireEvent.click(screen.getByRole('button', { name: copyLabel() }));
    await waitFor(() => expect(success).toHaveBeenCalledWith(i18next.t('apiExplorer.copied')));
    expect(copy).toHaveBeenCalledExactlyOnceWith(value);
  });

  it.each(['refused', 'rejected'] as const)('reports a %s copy without claiming success', async (result) => {
    const copy = vi.spyOn(ClipboardManager, 'copyText');
    if (result === 'refused') copy.mockResolvedValue(false); else copy.mockRejectedValue(new Error('Clipboard denied'));
    const error = vi.spyOn(getMessage(), 'error').mockImplementation(vi.fn());
    const success = vi.spyOn(getMessage(), 'success').mockImplementation(vi.fn());
    render(<ApiCodeBlock value="read-only" />);
    fireEvent.click(screen.getByRole('button', { name: copyLabel() }));
    await waitFor(() => expect(error).toHaveBeenCalledWith(i18next.t('apiExplorer.copyFailed')));
    expect(success).not.toHaveBeenCalled();
  });

  it('renders hostile code and labels only as text', () => {
    const hostile = '<img src=x onerror="alert(1)"><script>alert(2)</script>';
    const value = JSON.stringify({ payload: hostile });
    const view = render(<ApiCodeBlock value={value} language="json" label={hostile} />);
    expect(view.container.querySelector('img,script')).toBeNull();
    expect(view.container.querySelector('code')?.textContent).toBe(value);
    expect(screen.getByRole('group', { name: hostile })).not.toBeNull();
    view.rerender(<ApiCodeBlock value={hostile} language="html" />);
    expect(view.container.querySelector('img,script')).toBeNull();
    expect(view.container.querySelector('code')?.textContent).toBe(hostile);
  });

  it.each([['js', 'JavaScript'], ['py', 'Python'], ['sh', 'Shell'], ['bash', 'Bash'], ['curl', 'cURL']])('labels %s readably and leaves its text intact', (language, name) => {
    const value = 'fetch("/panel/api/me");\n# literal code';
    const view = render(<ApiCodeBlock value={value} language={language} />);
    expect(screen.getByRole('group', { name })).not.toBeNull();
    expect(view.container.querySelector('code')?.textContent).toBe(value);
    expect(view.container.querySelectorAll('[class^="api-code-token-"]')).toHaveLength(0);
  });

  it('defaults to plain text and updates the value used by Copy', async () => {
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    vi.spyOn(getMessage(), 'success').mockImplementation(vi.fn());
    const view = render(<ApiCodeBlock value="first" />);
    expect(screen.getByRole('group', { name: i18next.t('apiCode.plainText') }).tabIndex).toBe(0);
    view.rerender(<ApiCodeBlock value={'next\n'} />);
    fireEvent.click(screen.getByRole('button', { name: copyLabel() }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('next\n'));
    expect(view.container.querySelector('code')?.textContent).toBe('next\n');
  });

  it.each(['length', 'tokens'] as const)('bounds %s highlighting without truncating content', (limit) => {
    const value = limit === 'length' ? JSON.stringify({ data: 'a'.repeat(40_000) }) : `[${Array.from({ length: 1000 }, (_, index) => String(index)).join(',')}]`;
    const view = render(<ApiCodeBlock value={value} language="json" />);
    expect(view.container.querySelectorAll('[class^="api-code-token-"]')).toHaveLength(0);
    expect(view.container.querySelector('code')?.textContent).toBe(value);
  });

  it('renders many expanded blocks with independent accessible labels and no editors', () => {
    const view = render(<div>{Array.from({ length: 267 }, (_, index) => <ApiCodeBlock key={index} label={`Operation ${index}`} value={`{"id":${index}}`} language="json" />)}</div>);
    expect(screen.getAllByRole('group')).toHaveLength(267);
    const last = screen.getByRole('group', { name: 'Operation 266' });
    expect(last.textContent).toBe('{"id":266}');
    expect(within(last.parentElement!).getByRole('button', { name: copyLabel() })).not.toBeNull();
    expect(view.container.querySelector('textarea,input,[contenteditable],.cm-editor')).toBeNull();
  });

});
