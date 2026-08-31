import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, waitFor, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import { renderWithProviders } from './test-utils';

/*
A rejected value is reported on the field that holds it.

The whole form was validated in one safeParse and only the FIRST issue was
shown, as a page-level toast: the operator was told a value was wrong and not
which of eighteen inputs held it, across three tabs of which one is on screen.
F178.
*/

const INBOUNDS = [
  { id: 1, port: 443, protocol: 'trojan', tag: 'in-443', enable: true },
] as unknown as InboundOption[];

const CLIENT = { email: 'testuser', enable: true } as unknown as ClientRecord;

// The en-US string for pages.clients.emailInvalidChars, backslash and all.
const REJECTED_EMAIL = String.raw`Email cannot contain spaces, '/', '\', or control characters`;

function renderModal(save = vi.fn().mockResolvedValue(null)) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    const obj = url === '/panel/api/cores'
      ? [{
        id: 'xray',
        titleKey: 'cores.xray.title',
        kinds: ['trojan'],
        caps: {},
        clientCredentials: { trojan: ['password'] },
      }]
      : {};
    return { success: true, obj } as never;
  });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithProviders(
    <QueryClientProvider client={queryClient}>
      <ClientFormModal
        open
        mode="edit"
        client={CLIENT}
        inbounds={INBOUNDS}
        attachedIds={[1]}
        save={save}
        onOpenChange={() => {}}
      />
    </QueryClientProvider>,
  );
  return save;
}

afterEach(() => {
  vi.restoreAllMocks();
});

function submit() {
  const button = Array.from(document.querySelectorAll('button'))
    .find((b) => (b.textContent ?? '').trim() === 'Save');
  if (!button) throw new Error('Save button not found');
  fireEvent.click(button);
}

describe('the client form reports a rejected value on its own field', () => {
  it('marks the email field rather than raising a toast', async () => {
    const save = renderModal();
    await waitFor(() => {
      const labels = Array.from(document.querySelectorAll('button')).map((b) => (b.textContent ?? '').trim());
      expect(labels, 'the manifest never arrived, so the form offers a retry instead of Save').toContain('Save');
    });
    const emailInput = screen.getByPlaceholderText('Email');
    fireEvent.change(emailInput, { target: { value: 'has space' } });
    submit();

    await waitFor(() => {
      const item = emailInput.closest('.ant-form-item');
      expect(item, 'the email input is not inside a Form.Item, so nothing can mark it').not.toBeNull();
      expect(
        (item as HTMLElement).className,
        'the email field is not marked as holding the rejected value',
      ).toContain('ant-form-item-has-error');
      const shown = Array.from((item as HTMLElement).querySelectorAll('.ant-form-item-explain'))
        .map((n) => (n.textContent ?? '').trim());
      expect(
        shown,
        'the reason was reported somewhere other than on the email field',
      ).toContain(REJECTED_EMAIL);
    });
    expect(save).not.toHaveBeenCalled();
  });
});
