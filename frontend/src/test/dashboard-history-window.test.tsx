import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { HistoryWindowControl } from '@/pages/index/DashboardTelemetry';
import { useOverviewHistory } from '@/pages/index/useOverviewHistory';
import { Status } from '@/models/status';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';
import i18next from 'i18next';

function HistoryHarness() {
  const [bucket, setBucket] = useState(60);
  const history = useOverviewHistory(new Status(), true, bucket);
  const times = history.times ?? [];
  return <>
    <HistoryWindowControl value={bucket} onChange={setBucket} />
    <output aria-label="window seconds">{times.length ? times[times.length - 1] - times[0] + bucket : 0}</output>
  </>;
}

afterEach(() => vi.restoreAllMocks());

it('requests fifteen-second buckets for the fifteen-minute dashboard window', async () => {
  const get = vi.spyOn(HttpUtil, 'get').mockImplementation((url) => Promise.resolve(new Msg(true, '', {
    bucket: Number(url.split('/').at(-1)), end: 900, series: { cpu: [{ t: 900, v: 20 }] },
  })));
  renderWithProviders(<HistoryHarness />);
  await waitFor(() => expect(get).toHaveBeenCalledWith('/panel/api/server/historyWindow/60', undefined, { silent: true }));
  fireEvent.click(screen.getByRole('radio', { name: i18next.t('dashboardRepair.window15m') }));
  await waitFor(() => expect(get).toHaveBeenCalledWith('/panel/api/server/historyWindow/15', undefined, { silent: true }));
  await waitFor(() => expect(screen.getByLabelText('window seconds').textContent).toBe('900'));
});
