import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import type { ComponentProps } from 'react';

import ClientPlanPanel, { POLICY_UNLOADED } from '@/pages/clients/ClientPlanPanel';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import { IpLimitControl } from '@/components/clients/IpLimitControl';
import { HttpUtil, Msg } from '@/utils';
import type { CoreView } from '@/generated/zod';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const WG_CORE: CoreView = {
  id: 'wireguard',
  titleKey: 'cores.wireguard.title',
  kinds: ['wgkernel'],
  caps: {},
  available: true,
  unavailable: '',
  clientCredentials: {},
  shaping: { wgkernel: 'innerIP' },
};

const XRAY_CORE: CoreView = { ...WG_CORE, id: 'xray', kinds: ['vless'], shaping: {} };

const PLAN = { id: 1, name: 'fair use', tiers: [], createdAt: 0, updatedAt: 0 };

const ENFORCED = {
  email: 'user1',
  usedBytes: 53687091200,
  policyId: 1,
  unresolved: false,
  shapeable: true,
  wantUpBps: 10000000,
  wantDownBps: 10000000,
  enforcedUpBps: 10000000,
  enforcedDownBps: 10000000,
};

function mockHost(enforced: unknown, ok = true, cores: CoreView[] = [WG_CORE]) {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => Promise.resolve(
    url === '/panel/api/policies/list' ? new Msg(true, '', [PLAN])
      : url === '/panel/api/cores' ? new Msg(true, '', cores)
        : url.startsWith('/panel/api/policies/enforced/')
          ? (ok ? new Msg(true, '', enforced) : new Msg(false, 'no such user'))
          : new Msg(true, '', {}),
  ));
}

function Harness({ kinds, cores }: { kinds: string[]; cores: CoreView[] }) {
  const [value, setValue] = useState(POLICY_UNLOADED);
  return (
    <ClientPlanPanel
      email="user1"
      kinds={kinds}
      cores={cores}
      value={value}
      onSeed={setValue}
      onSelect={setValue}
    />
  );
}

async function renderPanel(kinds: string[], cores: CoreView[]) {
  renderWithProviders(<Harness kinds={kinds} cores={cores} />);
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

describe('ClientPlanPanel', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('shows what the plan asks for beside what the kernel is holding', async () => {
    mockHost(ENFORCED);
    await renderPanel(['wgkernel'], [WG_CORE]);

    const text = document.body.textContent ?? '';
    expect(text).toContain(enUS.pages.policies.inEffect);
    expect(text).toContain(enUS.pages.policies.enforced);
    expect(text).toContain('10 Mbps');
    expect(text).toContain('50.00 GB');
  });

  /* A protocol whose core cannot tell one client from another in the kernel is
     a stated limitation, not a field that quietly does nothing. */
  it('says speed limits are unavailable on a protocol no core can shape', async () => {
    mockHost({ ...ENFORCED, shapeable: false, policyId: 0 });
    await renderPanel(['vless'], [XRAY_CORE]);

    expect(document.body.textContent).toContain(enUS.pages.policies.notShapeable);
  });

  it('a client on a shapeable and an unshapeable inbound is told which one a rate reaches', async () => {
    mockHost(ENFORCED);
    await renderPanel(['wgkernel', 'vless'], [WG_CORE, XRAY_CORE]);

    const text = document.body.textContent ?? '';
    expect(text).toContain(enUS.pages.policies.partlyShapeable);
    expect(text).not.toContain(enUS.pages.policies.notShapeable);
  });

  /* Two shaped inbounds each get the contracted rate, which is the v1 semantic
     and the one thing an operator cannot infer from the number itself. */
  it('says the rate lands per inbound when the client has two shaped ones', async () => {
    mockHost(ENFORCED);
    await renderPanel(['wgkernel', 'wgkernel'], [WG_CORE]);

    expect(document.body.textContent).toContain(enUS.pages.policies.perInbound);
  });

  it('a plan that no longer exists is reported rather than read as no plan', async () => {
    mockHost({ ...ENFORCED, unresolved: true, wantUpBps: 0, wantDownBps: 0 });
    await renderPanel(['wgkernel'], [WG_CORE]);

    expect(document.body.textContent).toContain(enUS.pages.policies.unresolved);
  });

  /* Shapeable is computed from live wants, so false also means "the device is
     down" — which must not read as a rate the kernel is holding. */
  it('a limit that never landed reads as not enforced, not as a number', async () => {
    mockHost({ ...ENFORCED, shapeable: false });
    await renderPanel(['wgkernel'], [WG_CORE]);

    expect(document.body.textContent).toContain(enUS.pages.policies.notEnforcedYet);
  });

  it('offers a retry rather than guessing when the assignment cannot be read', async () => {
    mockHost(null, false);
    await renderPanel(['wgkernel'], [WG_CORE]);

    expect(document.body.textContent).toContain(enUS.pages.policies.readbackFailed);
    expect(document.querySelector('.ant-select')).toBeNull();
  });
});

function IpHarness({ initial }: { initial: number }) {
  const [value, setValue] = useState(initial);
  return <IpLimitControl value={value} onChange={setValue} />;
}

describe('IpLimitControl', () => {
  /* Zero means "no cap" and a bare number field reads it as "allow nothing". */
  it('an unlimited cap says so, and shows no number to misread', async () => {
    renderWithProviders(<IpHarness initial={0} />);
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

    const selected = document.querySelector('.ant-segmented-item-selected');
    expect(selected?.textContent).toBe(enUS.pages.policies.unlimited);
    expect(document.querySelector('.ant-input-number')).toBeNull();
  });

  it('a real cap shows its number', async () => {
    renderWithProviders(<IpHarness initial={3} />);
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

    const selected = document.querySelector('.ant-segmented-item-selected');
    expect(selected?.textContent).toBe(enUS.pages.clients.limitIpCapped);
    expect(document.querySelector<HTMLInputElement>('.ant-input-number input')?.value).toBe('3');
  });

  /* Picking "Limit to" must land on a cap that means something; leaving it at
     zero would store unlimited under a label that says the opposite. */
  it('switching to a cap never leaves the stored value at unlimited', async () => {
    renderWithProviders(<IpHarness initial={0} />);
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

    const capped = Array.from(document.querySelectorAll('.ant-segmented-item'))
      .find((el) => el.textContent === enUS.pages.clients.limitIpCapped);
    await act(async () => { fireEvent.click(capped!); });

    const input = document.querySelector<HTMLInputElement>('.ant-input-number input');
    expect(Number(input?.value)).toBeGreaterThan(0);
  });

  it('a host that cannot enforce the cap says so instead of showing it as unlimited', async () => {
    renderWithProviders(
      <IpLimitControl value={0} onChange={() => {}} disabled notice="Fail2ban is not installed" />,
    );
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

    expect(document.body.textContent).toContain('Fail2ban is not installed');
    expect(document.querySelector('.ant-segmented-disabled')).not.toBeNull();
  });
});

const WG_INBOUND = {
  id: 4,
  port: 51820,
  protocol: 'wgkernel',
  tag: 'in-51820-wg',
  enable: true,
} as unknown as InboundOption;

const CLIENT = {
  email: 'user1',
  uuid: '11111111-1111-1111-1111-111111111111',
  subId: 'subid123',
  enable: true,
} as unknown as ClientRecord;

function planSelect(): HTMLElement {
  const label = Array.from(document.querySelectorAll('.ant-form-item-label label'))
    .find((el) => (el.textContent ?? '').includes(enUS.pages.policies.plan));
  const select = label?.closest('.ant-form-item')?.querySelector('.ant-select');
  if (!select) throw new Error('the plan select is not on screen');
  return (select.querySelector('.ant-select-selector') ?? select) as HTMLElement;
}

type SaveProp = ComponentProps<typeof ClientFormModal>['save'];
type SaveSpy = SaveProp & { mock: { calls: unknown[][] } };

/* vi.fn()'s own type is a constructable union the prop will not accept, so the
   spy is named as the prop it stands in for. */
function makeSave(): SaveSpy {
  return vi.fn().mockResolvedValue({ success: true }) as unknown as SaveSpy;
}

async function openLimitsTab(save: SaveSpy) {
  renderWithProviders(
    <ClientFormModal
      open
      mode="edit"
      client={CLIENT}
      inbounds={[WG_INBOUND]}
      attachedIds={[4]}
      save={save}
      onOpenChange={() => {}}
    />,
  );
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  const tab = await screen.findByRole('tab', { name: enUS.pages.clients.tabLimits });
  await act(async () => { fireEvent.click(tab); });
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

async function saveForm() {
  const button = await screen.findByRole('button', { name: /save/i });
  await act(async () => { fireEvent.click(button); });
  await waitFor(() => expect(document.body).toBeTruthy());
}

function assignedPlan(save: SaveSpy): number | undefined {
  return (save.mock.calls[0]?.[1] as { policyId?: number } | undefined)?.policyId;
}

describe('ClientFormModal — the plan rides its own endpoint', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  /* The assignment is only ever read on the Limits tab, so a save that never
     opened it must leave it alone rather than write back a value it invented. */
  it('a save that never read the assignment does not rewrite it', async () => {
    mockHost(ENFORCED);
    const save = makeSave();
    renderWithProviders(
      <ClientFormModal open mode="edit" client={CLIENT} inbounds={[WG_INBOUND]} attachedIds={[4]} save={save} onOpenChange={() => {}} />,
    );
    for (let i = 0; i < 6; i += 1) {
      await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
    }
    await saveForm();

    expect(save).toHaveBeenCalled();
    expect(assignedPlan(save)).toBeUndefined();
  });

  it('an unchanged plan is not written back either', async () => {
    mockHost(ENFORCED);
    const save = makeSave();
    await openLimitsTab(save);
    await saveForm();

    expect(assignedPlan(save)).toBeUndefined();
  });

  it('taking a client off their plan sends the assignment, not a client field', async () => {
    mockHost(ENFORCED);
    const save = makeSave();
    await openLimitsTab(save);

    await act(async () => { fireEvent.mouseDown(planSelect()); });
    const option = Array.from(document.querySelectorAll('.ant-select-item-option'))
      .find((o) => (o.textContent ?? '').trim() === enUS.pages.policies.noPlan);
    await act(async () => { fireEvent.click(option!); });
    await saveForm();

    expect(assignedPlan(save)).toBe(0);
    expect(save.mock.calls[0][0]).not.toHaveProperty('policyId');
  });
});
