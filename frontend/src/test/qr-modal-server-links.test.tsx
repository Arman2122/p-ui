import { act } from '@testing-library/react';
import { describe, expect, it, vi, afterEach } from 'vitest';

import QrCodeModal from '@/pages/inbounds/qr/QrCodeModal';
import { ClipboardManager, HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

/*
The panel's QR modal must show the server's links, and must show a failure as a
failure.

It used to build links in the browser, which has no concept of a Host row, so a
CDN-fronted inbound produced a QR pointing at the origin — from the same page
that lists the hosts. The fix moves the render to the server; the trap it opens
is a fallback, because falling back to the local builder puts the origin address
back on screen at the exact moment the server is signalling a problem.
*/
const hostedInbound = {
  id: 12,
  port: 4451,
  listen: '203.0.113.5',
  protocol: 'vless',
  remark: 'edge-1',
  settings: {
    clients: [{ id: 'c9f0c2d0-0000-4000-8000-000000000000', email: 'alice@example.test' }],
    decryption: 'none',
  },
  streamSettings: { network: 'ws', security: 'tls', wsSettings: { path: '/base' } },
  sniffing: {},
};

function renderQr() {
  return renderWithProviders(
    <QrCodeModal open onClose={() => {}} dbInbound={hostedInbound} client={null} subSettings={undefined} />,
  );
}

afterEach(() => { vi.restoreAllMocks(); });

describe('QR modal link source', () => {
  it('shows the host address the server rendered, not the origin', async () => {
    const hostLink = 'vless://c9f0c2d0-0000-4000-8000-000000000000@a.cdn.com:2096?security=tls&type=ws#edge-1-alice';
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, msg: '', warning: '', obj: [hostLink] });
    const copied = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);

    renderQr();
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

    // The link itself only reaches the DOM as a QR image, so the copy button is
    // what proves which address the operator actually walks away with.
    const copy = document.querySelector('.qr-panel-header button') as HTMLElement;
    expect(copy).toBeTruthy();
    await act(async () => { copy.click(); });
    expect(copied).toHaveBeenCalledWith(hostLink);
    expect(document.body.textContent ?? '').not.toContain('203.0.113.5');
  });

  it('shows the failure instead of falling back to a locally built link', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({
      success: false,
      msg: 'inbound not found',
      warning: '',
      obj: null,
    });

    renderQr();
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

    expect(document.body.textContent ?? '').toContain('inbound not found');
    // No link panel at all. A fallback would have rendered one, from the origin,
    // looking exactly like a success.
    expect(document.querySelectorAll('.qr-panel')).toHaveLength(0);
    expect(document.body.textContent ?? '').not.toContain('203.0.113.5');
  });
});
