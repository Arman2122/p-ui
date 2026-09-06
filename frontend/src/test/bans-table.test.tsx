import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';

import { BansTable } from '@/components/clients/BansTable';
import type { BanView } from '@/generated/zod';

import { renderWithProviders } from './test-utils';

const until = 1_788_700_000;
const rows: BanView[] = [
  { id: 1, clientEmail: 'alice', source: '203.0.113.7', ports: '443,8443', reason: 'over-cap', node: '', origin: '', createdAt: until - 300, until },
  { id: 2, clientEmail: 'bob', source: '198.51.100.9', ports: '2053', reason: 'over-cap', node: 'de-fra-1', origin: '', createdAt: until - 300, until },
  { id: 3, clientEmail: 'carol', source: '203.0.113.7', ports: '', reason: 'shared-skipped', node: '', origin: '', createdAt: until - 300, until },
];

/*
The table is the operator's only view of who is dropped where. A release must
hand back exactly the pair it was clicked on, and a row that is a report, not a
ban, must read as one rather than as a ban with no port.
*/
describe('BansTable', () => {
  it('releases the pair of the row clicked, and names the node a ban is held on', () => {
    const onRelease = vi.fn();
    renderWithProviders(<BansTable bans={rows} onRelease={onRelease} onReleaseAll={() => {}} />);
    expect(screen.getByText('de-fra-1')).toBeTruthy();
    /* The exact name: "Release all" is a button too, and must not be counted. */
    const buttons = screen.getAllByRole('button', { name: /^release$/i });
    fireEvent.click(buttons[1]);
    expect(onRelease).toHaveBeenCalledWith('bob', '198.51.100.9');
  });

  it('shows a reported shared address as a report, not as a ban', () => {
    renderWithProviders(<BansTable bans={rows} />);
    expect(screen.getByText(/reported, not banned/i)).toBeTruthy();
    expect(screen.queryAllByRole('button', { name: /^release$/i })).toHaveLength(0);
  });
});
