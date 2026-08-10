import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';

import { HttpUtil } from '@/utils';
import InboundFormModal from '@/pages/inbounds/form/InboundFormModal';
import { renderWithProviders } from './test-utils';

/* A core that fails Preflight answers `available: false`, and offering its kinds
   anyway hands out configs for a daemon or device that never exists here. */

/* Driven through mtproto because AntD virtualises the dropdown and renders only
   the first ten options in jsdom; the gate itself is one kind-agnostic lookup. */
const REASON = 'mtproto: mtg binary not found';

function serveCores(mtprotoAvailable: boolean) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    const obj = url === '/panel/api/cores'
      ? [
        { id: 'xray', titleKey: 'cores.xray.title', kinds: ['vless', 'wireguard'], caps: {}, clientCredentials: {}, available: true, unavailable: '' },
        { id: 'mtproto', titleKey: 'cores.mtproto.title', kinds: ['mtproto'], caps: {}, clientCredentials: {}, available: mtprotoAvailable, unavailable: mtprotoAvailable ? '' : REASON },
      ]
      : {};
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    return { success: true, obj } as any;
  });
}

async function openProtocolPicker() {
  renderWithProviders(
    <InboundFormModal open mode="add" dbInbound={null} dbInbounds={[]} onClose={() => {}} onSaved={() => {}} />,
  );
  const selector = document.querySelector('#protocol');
  if (!selector) throw new Error('protocol select not rendered');
  fireEvent.mouseDown(selector);
  await waitFor(() => {
    if (!document.querySelector('.ant-select-item-option')) throw new Error('dropdown not open');
  });
}

function optionFor(kind: string): HTMLElement {
  const option = Array.from(document.querySelectorAll('.ant-select-item-option'))
    .find((o) => (o.textContent ?? '').trim() === kind);
  if (!option) throw new Error(`no ${kind} option in the picker`);
  return option as HTMLElement;
}

describe('InboundFormModal — the picker asks each core whether this host can run it', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = '';
  });

  it('disables a kind whose core failed Preflight, and carries the reason', async () => {
    serveCores(false);
    await openProtocolPicker();
    await waitFor(() => {
      expect(optionFor('mtproto').getAttribute('aria-disabled')).toBe('true');
    });
    expect(optionFor('mtproto').getAttribute('title')).toBe(REASON);
    expect(optionFor('vless').getAttribute('aria-disabled')).not.toBe('true');
  });

  it('offers the same kind on a host whose core reports it usable', async () => {
    serveCores(true);
    await openProtocolPicker();
    await waitFor(() => {
      expect(optionFor('vless')).toBeTruthy();
    });
    expect(optionFor('mtproto').getAttribute('aria-disabled')).not.toBe('true');
  });
});
