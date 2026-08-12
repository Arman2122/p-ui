import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from '@testing-library/react';

import InboundFormModal from '@/pages/inbounds/form/InboundFormModal';
import { DBInbound } from '@/models/dbinbound';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const OFF = 'net.ipv4.ip_forward is 0, so no L3 inbound on this host forwards a packet of that family at all, egress or not';

function mockHost(forwardingNotes: string[]) {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url === '/panel/api/egresses/preflight'
      ? new Msg(true, '', { ok: true, refusals: [], notes: [], forwardingNotes })
      : new Msg(true, '', url === '/panel/api/egresses/list' ? [] : {}),
  ));
}

function wgkernelInbound(nodeId: number | null) {
  return new DBInbound({
    id: 7,
    port: 51820,
    listen: '',
    protocol: 'wgkernel',
    remark: 'tunnel',
    enable: true,
    settings: { secretKey: '', address: ['10.0.0.1/24'], mtu: 1420, dns: '', clients: [] },
    streamSettings: { security: 'none' },
    sniffing: { enabled: false },
    nodeId,
  });
}

async function renderForm(nodeId: number | null, forwardingNotes: string[]) {
  mockHost(forwardingNotes);
  renderWithProviders(
    <InboundFormModal
      open
      mode="edit"
      dbInbound={wgkernelInbound(nodeId)}
      dbInbounds={[]}
      availableNodes={[]}
      onClose={() => {}}
      onSaved={() => {}}
    />,
  );
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

function forwardingAlert(): HTMLElement {
  const title = enUS.pages.inbounds.form.wgkernelForwardingTitle;
  const alert = Array.from(document.querySelectorAll('.ant-alert'))
    .find((el) => (el.textContent ?? '').includes(title));
  if (!alert) throw new Error('the forwarding alert is not rendered');
  return alert as HTMLElement;
}

/*
A fresh install has ip_forward=0 and no NAT, so the first wgkernel inbound
anyone creates completes handshakes and routes nothing. The panel has always
been able to read the knob — for an egress — and never said so here.
*/
describe('wgkernel forwarding notice', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('says so, in the host\'s own words, when this host forwards nothing', async () => {
    await renderForm(null, [OFF]);

    const alert = forwardingAlert();
    expect(alert.className).toContain('ant-alert-error');
    expect(alert.textContent).toContain(enUS.pages.inbounds.form.wgkernelForwardingOff);
    // The sentence names the sysctl, so it is rendered and never translated.
    expect(alert.textContent).toContain('net.ipv4.ip_forward is 0');
  });

  it('stays advisory on a host that already forwards', async () => {
    await renderForm(null, []);

    const alert = forwardingAlert();
    expect(alert.className).toContain('ant-alert-warning');
    expect(alert.textContent).not.toContain(enUS.pages.inbounds.form.wgkernelForwardingOff);
  });

  // A node's inbound is served somewhere this panel cannot read a sysctl from,
  // so reporting THIS host's knobs against it would be a lie about another box.
  it('never reports this host against an inbound deployed to a node', async () => {
    await renderForm(3, [OFF]);

    expect(forwardingAlert().textContent).not.toContain('net.ipv4.ip_forward is 0');
  });
});
