import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, fireEvent, screen } from '@testing-library/react';

import PoliciesPage from '@/pages/policies/PoliciesPage';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { makeTestQueryClient, renderWithProviders } from './test-utils';

const LADDER = [
  { fromBytes: 0, upBps: 0, downBps: 0 },
  { fromBytes: 53687091200, upBps: 10000000, downBps: 10000000 },
  { fromBytes: 107374182400, upBps: 2000000, downBps: 2000000 },
];

const PLAN = { id: 1, name: 'fair use', tiers: LADDER, rev: 0, createdAt: 0, updatedAt: 0 };

const WG_CORE = {
  id: 'wireguard',
  titleKey: 'cores.wireguard.title',
  kinds: ['wgkernel'],
  caps: {},
  available: true,
  unavailable: '',
  clientCredentials: {},
  shaping: { wgkernel: 'innerIP' },
  applyDelaySeconds: {}, exitKinds: [], rateKinds: [], sessionKinds: [], portlessKinds: [],
};

const XRAY_CORE = { ...WG_CORE, id: 'xray', kinds: ['vless'], shaping: {} };

function mockHost(plans: unknown[], cores: unknown[], reseller = false) {
  const base = reseller ? '/panel/api/reseller/plans' : '/panel/api/policies';
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url === `${base}/list` ? new Msg(true, '', plans)
      : url === '/panel/api/cores' ? new Msg(true, '', cores)
        : url === (reseller ? '/panel/api/reseller/inbounds' : '/panel/api/inbounds/options') ? new Msg(true, '', [])
          : url.includes('/members/') ? new Msg(true, '', { items: [], total: 0 })
            : url.endsWith('/summary') ? new Msg(true, '', { withoutPlan: 0 })
        : new Msg(true, '', {}),
  ));
}

async function renderPage(reseller = false, queryClient?: ReturnType<typeof makeTestQueryClient>) {
  renderWithProviders(<PoliciesPage reseller={reseller} />, { queryClient });
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

describe('PoliciesPage', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it.each([
    { role: 'administrator', reseller: false, query: 'planId=2&create=1' },
    { role: 'reseller', reseller: true, query: 'new=1&plan=2' },
  ])('opens the selected plan in the $role client creation flow', async ({ reseller, query }) => {
    const previousBasePath = window.PUI_BASE_PATH;
    window.PUI_BASE_PATH = '/private-panel/';
    try {
      mockHost([PLAN, { ...PLAN, id: 2, name: 'Premium' }], [WG_CORE], reseller);
      await renderPage(reseller);
      await act(async () => { fireEvent.click(screen.getByRole('button', { name: /Premium/ })); });
      expect(screen.getByRole('link', { name: new RegExp(`${enUS.plans.newClient}$`) }).getAttribute('href')).toBe(`/private-panel/panel/clients?${query}`);
      expect(screen.getByRole('button', { name: enUS.plans.archive }).hasAttribute('disabled')).toBe(false);
      expect(screen.getByRole('button', { name: new RegExp(`${enUS.plans.duplicate}$`) }).hasAttribute('disabled')).toBe(false);
      if (reseller) expect(screen.queryByRole('button', { name: enUS.plans.reapply })).toBeNull();
    } finally {
      if (previousBasePath === undefined) delete window.PUI_BASE_PATH;
      else window.PUI_BASE_PATH = previousBasePath;
    }
  });

  it('archives and reapplies through real catalogue endpoints', async () => {
    mockHost([PLAN], [WG_CORE]);
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation((url) => Promise.resolve(
      new Msg(true, '', url.includes('/reapply/') ? { applied: 3, needRestart: false, failures: [] } : {}),
    ));
    await renderPage();
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /^(Archive|plans.archive)$/i })); });
    expect(post).toHaveBeenCalledWith('/panel/api/policies/archive/1', { archived: true }, expect.anything());
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: /^(Reapply to members|plans.reapply)$/i })); });
    await act(async () => { fireEvent.click(screen.getByRole('checkbox')); });
    const confirms = screen.getAllByRole('button', { name: /^(Reapply to members|plans.reapply)$/i });
    await act(async () => { fireEvent.click(confirms[confirms.length - 1]); });
    expect(post).toHaveBeenCalledWith('/panel/api/policies/reapply/1', { clearOverrides: true }, expect.anything());
  });

  it('keeps archive and duplicate private and refreshes reseller plan choices', async () => {
    const cache = makeTestQueryClient();
    const choices = ['resellers', 'my-provisioning-plans'];
    const dashboardChoices = ['resellers', 'my-plan-choices'];
    cache.setQueryData(choices, [PLAN]);
    cache.setQueryData(dashboardChoices, [PLAN]);
    mockHost([PLAN], [], true);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', {}));
    await renderPage(true, cache);
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: enUS.plans.archive })); });
    expect(post).toHaveBeenCalledWith('/panel/api/reseller/plans/archive/1', { archived: true }, expect.anything());
    expect(cache.getQueryState(choices)?.isInvalidated).toBe(true);
    expect(cache.getQueryState(dashboardChoices)?.isInvalidated).toBe(true);
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: new RegExp(`${enUS.plans.duplicate}$`) })); });
    fireEvent.change(screen.getByRole('textbox', { name: enUS.pages.policies.name }), { target: { value: 'Private copy' } });
    const confirms = screen.getAllByRole('button', { name: new RegExp(`${enUS.plans.duplicate}$`) });
    await act(async () => { fireEvent.click(confirms[confirms.length - 1]); });
    expect(post).toHaveBeenCalledWith('/panel/api/reseller/plans/duplicate/1', { name: 'Private copy' }, expect.anything());
    expect(post.mock.calls.every(([url]) => url.startsWith('/panel/api/reseller/plans/'))).toBe(true);
    expect(screen.queryByRole('button', { name: enUS.plans.reapply })).toBeNull();
  });

  /* Zero bits per second is unlimited, and the whole point of the ladder is
     that an operator can read what each rung grants without decoding a 0. */
  it('spells every rung out, and never leaves a rate as a bare zero', async () => {
    mockHost([PLAN], [WG_CORE]);
    await renderPage();

    const row = document.querySelector('.p-plan-detail');
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

    const row = document.querySelector('.p-plan-detail');
    expect(row?.textContent).toContain(enUS.pages.policies.noTiersHint);
  });

  it('a ladder that starts above zero says the client is unlimited below it', async () => {
    mockHost([{ ...PLAN, tiers: [LADDER[1]] }], [WG_CORE]);
    await renderPage();

    const row = document.querySelector('.p-plan-detail');
    expect(row?.textContent).toContain(enUS.pages.policies.belowFirstHint);
  });

  it('keeps the visible plan and its details together after filtering the catalogue', async () => {
    mockHost([PLAN, { ...PLAN, id: 2, name: 'Premium', tiers: [{ fromBytes: 0, upBps: 50000000, downBps: 50000000 }] }], [WG_CORE]);
    await renderPage();
    fireEvent.change(screen.getByRole('searchbox', { name: enUS.design.searchPlans }), { target: { value: 'Premium' } });
    const detail = document.querySelector('.p-plan-detail');
    expect(detail?.textContent).toContain('Premium');
    expect(detail?.textContent).toContain('50 Mbps');
    expect(detail?.textContent).not.toContain('fair use');
  });
});
