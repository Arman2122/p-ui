import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, fireEvent } from '@testing-library/react';

import InboundFormModal from '@/pages/inbounds/form/InboundFormModal';
import { DBInbound } from '@/models/dbinbound';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const EGRESS = {
  id: 1,
  type: 'xray-tun',
  enable: true,
  remark: 'warp exit',
  target: 'warp',
  settings: '',
  createdAt: 0,
  updatedAt: 0,
};

function mockEgressList() {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url === '/panel/api/egresses/list'
      ? new Msg(true, '', [EGRESS])
      : new Msg(true, '', {}),
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

/* The egress list is fetched only once the form resets to the wgkernel protocol,
   so a single tick would assert against the DOM from before the query enabled. */
async function renderForm(nodeId: number | null) {
  mockEgressList();
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

async function flush() {
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

function openSelect(id: string): HTMLElement {
  const select = document.getElementById(id)?.closest('.ant-select') as HTMLElement | null;
  if (!select) throw new Error(`select '${id}' not rendered`);
  fireEvent.mouseDown((select.querySelector('.ant-select-selector') ?? select) as HTMLElement);
  return select;
}

/* rc-virtual-list renders one window of options and every row measures zero in
   jsdom, so a protocol past the first ten exists only after a scroll. */
function chooseProtocol(name: string) {
  const select = openSelect('protocol');
  const find = () => Array.from(document.querySelectorAll('.ant-select-item-option'))
    .find((o) => (o.textContent ?? '').trim() === name);
  const holder = document.querySelector('.rc-virtual-list-holder') as HTMLElement | null;
  for (const top of [400, 0, 200]) {
    if (find() || !holder) break;
    Object.defineProperty(holder, 'scrollTop', { value: top, writable: true, configurable: true });
    fireEvent.scroll(holder, { target: { scrollTop: top } });
  }
  const option = find();
  if (!option) throw new Error(`protocol option '${name}' not offered`);
  fireEvent.click(option);
  fireEvent.keyDown(select, { key: 'Escape' });
}

/* Matched on the egress's own remark rather than by position: every dropdown in
   the modal shares one option class, so the first match may be another list's. */
function pickEgress(remark: string) {
  openSelect('egress');
  const option = Array.from(document.querySelectorAll(
    '.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option',
  )).find((o) => (o.textContent ?? '').includes(remark));
  if (!option) throw new Error(`egress option '${remark}' not offered`);
  fireEvent.click(option);
}

/* AntD 6 marks a chosen value with `ant-select-content-has-value`; the plain
   content node is present either way and holds the placeholder when empty. */
function selectedEgressText(): string {
  const select = document.getElementById('egress')?.closest('.ant-select');
  return select?.querySelector('.ant-select-content-has-value')?.textContent ?? '';
}

/* The host commands the form tells the operator to run, in order. */
function alertCommands(): string[] {
  return Array.from(document.querySelectorAll('.ant-alert code')).map((c) => c.textContent ?? '');
}

function egressItem(): HTMLElement {
  const label = Array.from(document.querySelectorAll('.ant-form-item-label label'))
    .find((el) => (el.textContent ?? '').trim() === enUS.pages.inbounds.form.egress);
  const item = label?.closest('.ant-form-item') as HTMLElement | null;
  if (!item) throw new Error('egress form item not rendered');
  return item;
}

describe('kernel WireGuard egress attachment', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('offers the enabled egresses on a panel-hosted inbound', async () => {
    await renderForm(null);
    const item = egressItem();
    expect(item.querySelector('.ant-select-disabled')).toBeNull();
    expect(item.querySelector('.ant-form-item-extra')?.textContent)
      .toBe(enUS.pages.inbounds.form.egressHint);
  });

  /* The master-local refusal is enforced server-side; if the form let the
     operator ask for it anyway, the only feedback would be a failed save. */
  it('refuses a node-owned inbound and says why', async () => {
    await renderForm(3);
    const item = egressItem();
    expect(item.querySelector('.ant-select-disabled')).not.toBeNull();
    expect(item.querySelector('.ant-form-item-extra')?.textContent)
      .toBe(enUS.pages.inbounds.form.egressNodeOwned);
  });

  /* The egress terminates the connection itself, and the form already says so
     one field below -- while the alert above kept prescribing a NAT rule. */
  it('stops prescribing a masquerade rule once an egress is attached', async () => {
    await renderForm(null);
    expect(alertCommands()).toContain('iptables -t nat -A POSTROUTING -s 10.0.0.0/24 -j MASQUERADE');
    expect(alertCommands()).toContain('ip6tables -t nat -A POSTROUTING -s fd00::/64 -j MASQUERADE');

    pickEgress(EGRESS.remark);
    await flush();
    expect(alertCommands().filter((c) => c.includes('MASQUERADE'))).toEqual([]);
    expect(alertCommands()).toEqual([
      'sysctl -w net.ipv4.ip_forward=1',
      'sysctl -w net.ipv6.conf.all.forwarding=1',
    ]);
    expect(document.querySelector('.ant-alert')?.textContent)
      .toContain(enUS.pages.inbounds.form.wgkernelForwardingHintEgress);
  });

  /* Only an L3 ingress has a device to select on, so a selection left over from
     wgkernel would post an attach the server refuses after the inbound saved. */
  it('drops the selection when the new inbound stops being kernel WireGuard', async () => {
    mockEgressList();
    renderWithProviders(
      <InboundFormModal
        open
        mode="add"
        dbInbound={null}
        dbInbounds={[]}
        availableNodes={[]}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    chooseProtocol('wgkernel');
    await flush();
    pickEgress(EGRESS.remark);
    await flush();
    expect(selectedEgressText()).toContain(EGRESS.remark);

    chooseProtocol('vless');
    await flush();
    chooseProtocol('wgkernel');
    await flush();
    expect(selectedEgressText()).toBe('');
  });
});
