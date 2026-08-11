import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from '@testing-library/react';

import EgressesTab from '@/pages/xray/egresses/EgressesTab';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const REFUSAL = 'egress: net.ipv4.conf.all.rp_filter is 1, so the return path is dropped';
const NOTE = 'net.ipv4.ip_forward is 0, so no L3 inbound on this host forwards a packet at all';
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

function mockHost(preflight: { ok: boolean; refusals: string[]; notes: string[] }, rows: unknown[]) {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url === '/panel/api/egresses/preflight' ? new Msg(true, '', preflight)
      : url === '/panel/api/egresses/list' ? new Msg(true, '', rows)
        : new Msg(true, '', {}),
  ));
}

/* Both queries resolve on a microtask and repaint on a later macrotask, so a
   single tick would assert against the pre-fetch DOM and pass vacuously. */
async function renderTab() {
  renderWithProviders(<EgressesTab isMobile={false} />);
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

describe('EgressesTab', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  /* Refusals name a sysctl the panel cannot override, so they are shown verbatim
     and left-to-right: a translated or RTL-wrapped one is unactionable. */
  it('shows what stops this host carrying an egress, verbatim and LTR', async () => {
    mockHost({ ok: false, refusals: [REFUSAL], notes: [NOTE] }, []);
    await renderTab();

    const alerts = Array.from(document.querySelectorAll('.ant-alert'));
    const blocked = alerts.find((a) => a.textContent?.includes(enUS.pages.xray.egress.preflightBlocked));
    expect(blocked?.querySelector('[dir="ltr"]')?.textContent).toBe(REFUSAL);

    const notes = alerts.find((a) => a.textContent?.includes(enUS.pages.xray.egress.preflightNotes));
    expect(notes?.querySelector('[dir="ltr"]')?.textContent).toBe(NOTE);
  });

  it('lists the egresses and stays quiet on a host that can carry one', async () => {
    mockHost({ ok: true, refusals: [], notes: [] }, [EGRESS]);
    await renderTab();

    const row = document.querySelector('.ant-table-tbody tr.ant-table-row');
    expect(row?.textContent).toContain(EGRESS.remark);
    expect(row?.textContent).toContain(EGRESS.type);
    expect(row?.textContent).toContain(EGRESS.target);
    expect(document.querySelectorAll('.ant-alert')).toHaveLength(0);
  });
});
