import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import OutboundsTab from '@/pages/xray/outbounds/OutboundsTab';
import type { XraySettingsValue } from '@/hooks/useXraySetting';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const REFUSAL = 'egress: net.ipv4.conf.all.rp_filter is 1, so the return path is dropped';
const NOTE = 'net.ipv4.ip_forward is 0, so no L3 inbound on this host forwards a packet at all';

const UPLINK = {
  id: 4,
  type: 'wg-client' as const,
  enable: true,
  remark: 'mullvad',
  target: '',
  owner: 'operator',
  ingressInboundId: 0,
  settings: JSON.stringify({ endpoint: 'us-sfo.example.com:51820', address: ['10.14.0.2/32'] }),
  createdAt: 0,
  updatedAt: 0,
};

/* A front is provisioned and reaped by routing itself, so an operator never
   made it and has nothing to do with it. */
const PANEL_FRONT = {
  ...UPLINK,
  id: 9,
  type: 'xray-tun' as const,
  remark: 'front for inbound 7',
  target: 'warp',
  owner: 'panel',
  settings: '',
};

function mockHost(preflight: { ok: boolean; refusals: string[]; notes: string[] }, rows: unknown[]) {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url.includes('/egresses/preflight') ? new Msg(true, '', preflight)
      : url.includes('/egresses/list') ? new Msg(true, '', rows)
        : new Msg(true, '', {}),
  ));
}

function settings(): XraySettingsValue {
  return { outbounds: [{ tag: 'direct', protocol: 'freedom' }] } as unknown as XraySettingsValue;
}

async function renderTab() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithProviders(
    <QueryClientProvider client={queryClient}>
      <OutboundsTab
        templateSettings={settings()}
        setTemplateSettings={vi.fn()}
        outboundsTraffic={[]}
        outboundTestStates={{}}
        subscriptionTestStates={{}}
        testingAll={false}
        inboundTags={[]}
        isMobile={false}
        onResetTraffic={vi.fn()}
        onTest={vi.fn()}
        onTestSubscription={vi.fn()}
        onTestAll={vi.fn()}
        onShowWarp={vi.fn()}
        onShowNord={vi.fn()}
      />
    </QueryClientProvider>,
  );
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

/*
An exit is an answer to "where does traffic leave", which is the question the
outbounds table already asks — so it is a row in that table, not a second table
underneath it with its own heading.
*/
describe('exits share the outbounds table', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('lists an uplink beside the outbounds, by the endpoint it leaves through', async () => {
    mockHost({ ok: true, refusals: [], notes: [] }, [UPLINK]);
    await renderTab();

    const rows = Array.from(document.querySelectorAll('.ant-table-tbody tr.ant-table-row'));
    const body = rows.map((row) => row.textContent ?? '').join(' ');
    expect(body).toContain('direct');
    expect(body).toContain('mullvad');
    expect(body).toContain('us-sfo.example.com:51820');
  });

  it('hides the fronts routing provisions for itself', async () => {
    mockHost({ ok: true, refusals: [], notes: [] }, [UPLINK, PANEL_FRONT]);
    await renderTab();

    const body = document.querySelector('.ant-table-tbody')?.textContent ?? '';
    expect(body).toContain('mullvad');
    expect(body).not.toContain('front for inbound 7');
  });

  /* Refusals name a sysctl the panel cannot override, so they survive the move
     onto the merged table verbatim and left-to-right. */
  it('still says what stops this host carrying an exit', async () => {
    mockHost({ ok: false, refusals: [REFUSAL], notes: [NOTE] }, []);
    await renderTab();

    const alerts = Array.from(document.querySelectorAll('.ant-alert'));
    const blocked = alerts.find((a) => a.textContent?.includes(enUS.pages.xray.egress.preflightBlocked));
    expect(blocked?.querySelector('[dir="ltr"]')?.textContent).toBe(REFUSAL);

    const notes = alerts.find((a) => a.textContent?.includes(enUS.pages.xray.egress.preflightNotes));
    expect(notes?.querySelector('[dir="ltr"]')?.textContent).toBe(NOTE);
  });
});
