import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from '@testing-library/react';

import PoliciesPage from '@/pages/policies/PoliciesPage';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const LADDER = [
  { fromBytes: 0, upBps: 0, downBps: 0 },
  { fromBytes: 53687091200, upBps: 10000000, downBps: 10000000 },
  { fromBytes: 107374182400, upBps: 2000000, downBps: 2000000 },
];

const PLAN = { id: 1, name: 'fair use', tiers: LADDER, createdAt: 0, updatedAt: 0 };

const WG_CORE = {
  id: 'wireguard',
  titleKey: 'cores.wireguard.title',
  kinds: ['wgkernel'],
  caps: {},
  available: true,
  unavailable: '',
  clientCredentials: {},
  shaping: { wgkernel: 'innerIP' },
};

const XRAY_CORE = { ...WG_CORE, id: 'xray', kinds: ['vless'], shaping: {} };

function mockHost(plans: unknown[], cores: unknown[]) {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url === '/panel/api/policies/list' ? new Msg(true, '', plans)
      : url === '/panel/api/cores' ? new Msg(true, '', cores)
        : new Msg(true, '', {}),
  ));
}

async function renderPage() {
  renderWithProviders(<PoliciesPage />);
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

describe('PoliciesPage', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  /* Zero bits per second is unlimited, and the whole point of the ladder is
     that an operator can read what each rung grants without decoding a 0. */
  it('spells every rung out, and never leaves a rate as a bare zero', async () => {
    mockHost([PLAN], [WG_CORE]);
    await renderPage();

    const row = document.querySelector('.ant-table-tbody tr.ant-table-row');
    const text = row?.textContent ?? '';
    expect(text).toContain('fair use');
    expect(text).toContain(enUS.pages.policies.fromStart);
    expect(text).toContain('10 Mbps');
    expect(text).toContain('2 Mbps');
    expect(text).toContain(enUS.pages.policies.unlimited);
    expect(text).not.toMatch(/(?:^|\D)0 (?:Kbps|Mbps|Gbps)/);
  });

  it('says which protocols a rate can actually reach', async () => {
    mockHost([PLAN], [WG_CORE, XRAY_CORE]);
    await renderPage();

    const alert = document.querySelector('.ant-alert');
    expect(alert?.textContent).toContain(enUS.pages.policies.ratesReach);
    expect(alert?.textContent).toContain('wgkernel');
    expect(alert?.textContent).not.toContain('vless');
  });

  /* A build where nothing can be shaped must say so, not show a page that
     silently enforces nothing. */
  it('warns when no core in this build can carry a speed limit at all', async () => {
    mockHost([PLAN], [XRAY_CORE]);
    await renderPage();

    const alert = document.querySelector('.ant-alert');
    expect(alert?.textContent).toContain(enUS.pages.policies.ratesReachNothing);
    expect(alert?.className).toContain('ant-alert-warning');
  });

  it('a plan with no tiers says it limits nothing rather than showing an empty cell', async () => {
    mockHost([{ ...PLAN, tiers: [] }], [WG_CORE]);
    await renderPage();

    const row = document.querySelector('.ant-table-tbody tr.ant-table-row');
    expect(row?.textContent).toContain(enUS.pages.policies.noTiersHint);
  });

  it('a ladder that starts above zero says the client is unlimited below it', async () => {
    mockHost([{ ...PLAN, tiers: [LADDER[1]] }], [WG_CORE]);
    await renderPage();

    const row = document.querySelector('.ant-table-tbody tr.ant-table-row');
    expect(row?.textContent).toContain(enUS.pages.policies.belowFirstHint);
  });
});
