import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

/*
The update UI gave up 95 seconds into a run the backend budgets 20 minutes for.

pollUpdateStatus waited 5 s and then polled for 90 s before returning 'timeout',
which pops the "couldn't confirm the update finished" warning and reloads the
page. The backend's own constant is `updateStaleAfter = 20 * time.Minute`, with
a two-hour hard ceiling beyond it -- and a real update on a small VPS, which
fetches a release, unpacks it and restarts the service, routinely outlasts 95
seconds.

So an ordinary successful update told the operator it could not be confirmed,
mid-run, and reloaded the panel under it.

Read from the source on both sides, because the two numbers are in different
languages and nothing else would notice them drifting apart again. F278.
*/
describe('the panel update poll', () => {
  const modal = readFileSync(
    resolve(__dirname, '../pages/index/PanelUpdateModal.tsx'),
    'utf8',
  );
  const backend = readFileSync(
    resolve(__dirname, '../../../internal/web/service/panel/panel.go'),
    'utf8',
  );

  it('waits as long as the backend considers the run live', () => {
    const stale = backend.match(/updateStaleAfter\s*=\s*(\d+)\s*\*\s*time\.Minute/);
    expect(stale, 'updateStaleAfter is gone or reshaped; this guard is now vacuous').not.toBeNull();
    const backendMinutes = Number(stale![1]);

    const budget = modal.match(/UPDATE_POLL_BUDGET_MS\s*=\s*(\d+)\s*\*\s*60\s*\*\s*1_000/);
    expect(budget, 'UPDATE_POLL_BUDGET_MS is gone; this guard is now vacuous').not.toBeNull();
    const frontendMinutes = Number(budget![1]);

    expect(
      frontendMinutes,
      `the UI gives up after ${frontendMinutes} minute(s) while the backend treats the run as live for ${backendMinutes}; every update slower than that reports itself unconfirmed and reloads the page mid-run`,
    ).toBeGreaterThanOrEqual(backendMinutes);
  });

  it('no longer carries the 90-second deadline', () => {
    expect(modal).not.toContain('Date.now() + 90_000');
  });
});
