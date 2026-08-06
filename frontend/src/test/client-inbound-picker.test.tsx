import type { ReactElement } from 'react';
import { describe, it, expect, vi } from 'vitest';
import { cleanup, fireEvent } from '@testing-library/react';

import ClientBulkAddModal from '@/pages/clients/ClientBulkAddModal';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { InboundOption } from '@/hooks/useClients';
import { renderWithProviders } from './test-utils';

const ATTACHED_INBOUNDS_LABEL = 'Attached inbounds';

const INBOUNDS: InboundOption[] = [
  { id: 1, tag: 'in-1', remark: 'vless box', protocol: 'vless', enable: true },
  { id: 2, tag: 'in-2', remark: 'mtproto box', protocol: 'mtproto', enable: true },
  { id: 3, tag: 'in-3', remark: 'http proxy', protocol: 'http', enable: true },
];

/* Opens the attached-inbounds picker and returns the labels it offers. */
function openInboundPicker(): string[] {
  const label = Array.from(document.querySelectorAll('.ant-form-item-label label'))
    .find((el) => (el.textContent ?? '').trim() === ATTACHED_INBOUNDS_LABEL);
  const picker = label?.closest('.ant-form-item')?.querySelector('.ant-select');
  if (!picker) throw new Error(`no inbound picker under label: ${ATTACHED_INBOUNDS_LABEL}`);
  fireEvent.mouseDown(picker);
  return Array.from(
    document.querySelectorAll('.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option'),
  )
    .map((option) => (option.getAttribute('title') ?? option.textContent ?? '').trim())
    .filter(Boolean);
}

function inboundLabelsOffered(ui: ReactElement): string[] {
  renderWithProviders(ui);
  const offered = openInboundPicker();
  cleanup();
  document.body.innerHTML = '';
  return offered;
}

function bulkAddModal(): ReactElement {
  return (
    <ClientBulkAddModal open inbounds={INBOUNDS} onOpenChange={() => {}} />
  );
}

function singleAddModal(): ReactElement {
  return (
    <ClientFormModal
      open
      mode="add"
      client={null}
      inbounds={INBOUNDS}
      save={vi.fn().mockResolvedValue(null)}
      onOpenChange={() => {}}
    />
  );
}

describe('attached-inbounds picker', () => {
  it('offers mtproto inbounds in bulk add and still hides single-client protocols', () => {
    expect(inboundLabelsOffered(bulkAddModal())).toEqual(['vless box', 'mtproto box']);
  });

  it('offers the same inbounds in bulk add as in single add', () => {
    const single = inboundLabelsOffered(singleAddModal());
    expect(inboundLabelsOffered(bulkAddModal())).toEqual(single);
  });
});
