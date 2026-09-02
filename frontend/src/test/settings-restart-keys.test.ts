import { describe, expect, it } from 'vitest';

import { AllSetting } from '@/models/setting';
import { RESTART_KEYS, restartPendingKeys } from '@/lib/settings/restartKeys';

/*
The restart banner names the startup-only settings a save changed, and only those.

One blanket "restart the panel to apply changes" banner sat above every tab,
for settings that apply the moment they are saved and for the handful the
panel reads only when it starts alike. The page now compares the saved values
against the ones it loaded with, over the startup-only set. F402.
*/
describe('restart-only settings', () => {
  it('names a changed port and ignores a setting that applies on save', () => {
    const loaded = new AllSetting();
    const saved = new AllSetting({ ...loaded, webPort: 2054, tgBotEnable: !loaded.tgBotEnable });
    expect(restartPendingKeys(loaded, saved)).toEqual(['webPort']);
  });

  it('reports nothing when nothing restart-bound changed', () => {
    const loaded = new AllSetting();
    expect(restartPendingKeys(loaded, new AllSetting(loaded))).toEqual([]);
    expect(restartPendingKeys(undefined, loaded)).toEqual([]);
  });

  it('covers both servers: the panel listener and the subscription listener', () => {
    expect(RESTART_KEYS).toEqual(expect.arrayContaining(['webListen', 'webPort', 'webBasePath', 'subListen', 'subPort', 'subEnable']));
  });
});
