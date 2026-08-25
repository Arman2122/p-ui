import { describe, expect, it } from 'vitest';

import { getLimitIpNotice, type Fail2banStatus } from '@/api/queries/useFail2banStatusQuery';

/*
The IP-limit control has three ways to be unusable, and they need three
answers.

The probe behind `installed` was `fail2ban-client -h`, which the client's own
argparse answers even with the daemon stopped and p-ui's jail files deleted —
something the panel's own menu offers as an option. So a box with no jail
reported installed:true and the control stayed green while nothing was dropped.
`jailActive` is the state that decides whether a ban lands.
*/
const cases: { name: string; status: Fail2banStatus; want: string }[] = [
  {
    name: 'everything works',
    status: { enabled: true, installed: true, jailActive: true, usable: true },
    want: '',
  },
  {
    name: 'turned off on this server',
    status: { enabled: false, installed: false, jailActive: false, usable: false },
    want: 'pages.clients.limitIpDisabled',
  },
  {
    name: 'fail2ban is not installed at all',
    status: { enabled: true, installed: false, jailActive: false, usable: false },
    want: 'pages.clients.limitIpFail2banMissing',
  },
  {
    name: 'installed, but the jail is gone',
    status: { enabled: true, installed: true, jailActive: false, usable: false },
    want: 'pages.clients.limitIpJailMissing',
  },
];

describe('IP limit notice', () => {
  cases.forEach(({ name, status, want }) => {
    it(name, () => {
      expect(getLimitIpNotice(status, (key) => key)).toBe(want);
    });
  });
});
