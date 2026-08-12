import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, fireEvent, screen } from '@testing-library/react';

import PolicyFormModal from '@/pages/policies/PolicyFormModal';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const LADDER = [
  { fromBytes: 0, upBps: 0, downBps: 0 },
  { fromBytes: 53687091200, upBps: 10000000, downBps: 10000000 },
];

const PLAN = { id: 7, name: 'fair use', tiers: LADDER, createdAt: 0, updatedAt: 0 };

function mockWrites() {
  return vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', {}));
}

async function renderModal(policy: typeof PLAN | null) {
  renderWithProviders(<PolicyFormModal open policy={policy} onClose={() => {}} />);
  for (let i = 0; i < 4; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

async function save() {
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: /save/i })); });
  for (let i = 0; i < 4; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

function postedTiers(post: ReturnType<typeof mockWrites>) {
  return (post.mock.calls[0]?.[1] as { tiers?: unknown } | undefined)?.tiers;
}

describe('PolicyFormModal', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('seeds an existing ladder with its rates in the unit they were typed in', async () => {
    await renderModal(PLAN);

    const numbers = Array.from(document.querySelectorAll<HTMLInputElement>('.ant-input-number input'))
      .map((el) => el.value);
    expect(numbers).toContain('50');
    expect(numbers).toContain('10');
    expect(document.body.textContent).toContain('Mbps');
  });

  /* The tier the operator never limited must show as unlimited, both in the
     control and in the preview of what will be stored. */
  it('shows an unlimited tier as unlimited, in the control and in the preview', async () => {
    await renderModal(PLAN);

    const selected = Array.from(document.querySelectorAll('.ant-segmented-item-selected'))
      .map((el) => el.textContent);
    expect(selected).toContain(enUS.pages.policies.unlimited);
    expect(selected).toContain(enUS.pages.policies.capped);
    expect(document.body.textContent).toContain(enUS.pages.policies.preview);
    expect(document.body.textContent).toContain('10 Mbps');
  });

  /* The array is the shape the OpenAPI schema publishes and the shape the bind
     accepts; sending JSON text here would round-trip a string inside a string. */
  it('posts the ladder as an array, with unlimited written as zero', async () => {
    const post = mockWrites();
    await renderModal(PLAN);
    await save();

    expect(post).toHaveBeenCalledWith('/panel/api/policies/update/7', expect.anything(), expect.anything());
    expect(postedTiers(post)).toEqual(LADDER);
  });

  it('refuses two tiers at the same threshold rather than letting the backend drop one', async () => {
    const post = mockWrites();
    await renderModal({ ...PLAN, tiers: [LADDER[0]] });

    const addTier = Array.from(document.querySelectorAll('button'))
      .find((b) => (b.textContent ?? '').trim() === enUS.pages.policies.addTier);
    await act(async () => { fireEvent.click(addTier!); });
    await save();

    expect(post).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(enUS.pages.policies.thresholdDuplicate);
  });

  it('refuses a nameless plan', async () => {
    const post = mockWrites();
    await renderModal(null);
    await save();

    expect(post).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(enUS.pages.policies.nameRequired);
  });
});
