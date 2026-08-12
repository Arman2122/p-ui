import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, fireEvent } from '@testing-library/react';

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

function wgkernelWithPool(address: string[], clientAddrs: string[]) {
  return new DBInbound({
    id: 8,
    port: 51821,
    listen: '',
    protocol: 'wgkernel',
    remark: 'pool',
    enable: true,
    settings: {
      secretKey: '',
      address,
      mtu: 1420,
      dns: '',
      clients: clientAddrs.map((a, i) => ({ email: `c${i}@wgk`, allowedIPs: [a] })),
    },
    streamSettings: { security: 'none' },
    sniffing: { enabled: false },
    nodeId: null,
  });
}

async function renderPoolForm(address: string[], clientAddrs: string[]) {
  mockHost([]);
  renderWithProviders(
    <InboundFormModal
      open
      mode="edit"
      dbInbound={wgkernelWithPool(address, clientAddrs)}
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

/*
The allocator leaves the configured prefix silently once it fills, and every
client out there costs a kernel route rechecked on every pass. The form is the
only place an operator can learn that before it happens.
*/
describe('wgkernel pool capacity', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('says how much of the configured prefix is used', async () => {
    await renderPoolForm(['10.90.4.1/22'], ['10.90.4.2/32', '10.90.4.3/32']);

    // A /22 offers .2 through .7.255 — 1022 addresses, the number the sizing
    // advice quotes, so the form and the allocator agree.
    expect(document.body.textContent).toContain('2 of 1022 addresses used in 10.90.4.0/22');
  });

  it('names the clients that have already spilled outside it', async () => {
    await renderPoolForm(['10.90.4.1/22'], ['10.90.4.2/32', '10.90.0.9/32']);

    expect(document.body.textContent).toContain('1 of 1022 addresses used in 10.90.4.0/22');
    expect(document.body.textContent).toContain('1 client(s) are already outside it');
  });

  it('says nothing about a pool it cannot size', async () => {
    await renderPoolForm(['fd00::1/64'], []);

    expect(document.body.textContent).not.toContain('addresses used in');
  });
});

function addressSelect(): HTMLElement {
  const select = document.getElementById('wgkernelAddress')?.closest('.ant-select') as HTMLElement | null;
  if (!select) throw new Error('the interface address field is not rendered');
  return select;
}

async function typeAddress(value: string) {
  const input = addressSelect().querySelector('input') as HTMLInputElement;
  await act(async () => {
    fireEvent.change(input, { target: { value } });
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 13 });
  });
  for (let i = 0; i < 3; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

/*
The server parses each interface address and drops what fails IN SILENCE, so a
typo does not error — the device answers on nothing and no screen says why.
*/
describe('wgkernel interface address entry', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('completes a bare address instead of leaving it to be dropped', async () => {
    await renderPoolForm(['10.0.0.1/24'], []);
    await typeAddress('10.9.0.1');

    expect(addressSelect().textContent).toContain('10.9.0.1/24');
  });

  it('says why a typo was not added, and keeps what was already good', async () => {
    await renderPoolForm(['10.0.0.1/24'], []);
    await typeAddress('10.0.0.300');

    expect(document.body.textContent).toContain(enUS.pages.inbounds.form.wgkernelAddressInvalid.split('{')[0].trim());
    expect(addressSelect().textContent).toContain('10.0.0.1/24');
    expect(addressSelect().textContent).not.toContain('10.0.0.300');
  });

  // A /32 device address routes nobody: every client would land on a per-peer
  // route, which is the cost P9-A exists to keep an operator away from.
  it('refuses a device address with no room for a client', async () => {
    await renderPoolForm(['10.0.0.1/24'], []);
    await typeAddress('10.9.0.1/32');

    expect(document.body.textContent).toContain(enUS.pages.inbounds.form.wgkernelAddressNoRoom.split('{')[0].trim());
    expect(addressSelect().textContent).not.toContain('10.9.0.1/32');
  });
});
