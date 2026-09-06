import { describe, expect, it } from 'vitest';

import { ipLimitEnforcementOf, ipLimitFieldState } from '@/lib/cores/ip-limit';
import type { CoreView } from '@/generated/zod';

/*
The cap field used to be disabled whenever the firewall was unusable, for every
client alike — while a client on SSH, OpenVPN or OpenConnect has its cap refused
at the core's own login and needs no firewall at all. Each core now declares,
per kind, how a device over the cap is dealt with, and the form gates on that.
*/

const base: CoreView = {
  id: 'xray',
  titleKey: 'cores.xray.title',
  kinds: ['vless'],
  caps: {},
  available: true,
  unavailable: '',
  exitKinds: [],
  clientCredentials: {},
  shaping: {},
  sessionKinds: ['vless'],
  ipLimitEnforcement: { vless: 'firewall' },
  rateKinds: [],
  applyDelaySeconds: {},
  portlessKinds: [],
};

const SSH: CoreView = {
  ...base,
  id: 'ssh',
  titleKey: 'cores.ssh.title',
  kinds: ['ssh'],
  sessionKinds: ['ssh'],
  ipLimitEnforcement: { ssh: 'login' },
};

const SINGBOX: CoreView = {
  ...base,
  id: 'singbox',
  titleKey: 'cores.singbox.title',
  kinds: ['tuic'],
  sessionKinds: ['tuic'],
  ipLimitEnforcement: { tuic: 'cut' },
};

const MTPROTO: CoreView = {
  ...base,
  id: 'mtproto',
  titleKey: 'cores.mtproto.title',
  kinds: ['mtproto'],
  sessionKinds: [],
  ipLimitEnforcement: undefined,
};

const cores = [base, SSH, SINGBOX, MTPROTO];
const t = (key: string, options?: Record<string, string>) => `${key.split('.').pop()}(${options?.kinds ?? ''})`;
const HOST = 'host-notice';

describe('how each kind enforces the cap', () => {
  it('groups a client’s kinds by what their core declares', () => {
    expect(ipLimitEnforcementOf(cores, ['vless', 'ssh', 'tuic', 'mtproto'])).toEqual({
      login: ['ssh'],
      cut: ['tuic'],
      firewall: ['vless'],
    });
  });

  it('says nothing before the core list has loaded', () => {
    expect(ipLimitEnforcementOf(undefined, ['ssh'])).toEqual({ login: [], cut: [], firewall: [] });
  });
});

describe('whether the cap field is open', () => {
  it('a client on SSH alone keeps the field open on a host with no firewall', () => {
    expect(ipLimitFieldState(cores, ['ssh'], false, HOST, t)).toEqual({
      disabled: false,
      notice: 'limitIpEnforcedAtLogin(ssh)',
    });
  });

  it('a client on Xray alone is disabled by the host notice when the firewall is unusable', () => {
    expect(ipLimitFieldState(cores, ['vless'], false, HOST, t)).toEqual({ disabled: true, notice: HOST });
  });

  it('a client on Xray alone with the firewall usable says nothing', () => {
    expect(ipLimitFieldState(cores, ['vless'], true, HOST, t)).toEqual({ disabled: false, notice: '' });
  });

  /* The mixed client is the whole point: the SSH half holds the cap, so the
     field stays open, and the operator is told the Xray half needs the firewall. */
  it('a client on SSH and Xray stays open and names the half the firewall must cover', () => {
    expect(ipLimitFieldState(cores, ['ssh', 'vless'], false, HOST, t)).toEqual({
      disabled: false,
      notice: `limitIpEnforcedAtLogin(ssh) limitIpEnforcedByFirewall(vless) ${HOST}`,
    });
  });

  it('a cut-only kind is counted with the firewall, since its cut needs the ban behind it', () => {
    expect(ipLimitFieldState(cores, ['tuic'], false, HOST, t)).toEqual({ disabled: true, notice: HOST });
    expect(ipLimitFieldState(cores, ['ssh', 'tuic'], false, HOST, t).notice).toContain('limitIpEnforcedByFirewall(tuic)');
  });

  it('a client only on a kind that reports no sessions is disabled with that reason', () => {
    expect(ipLimitFieldState(cores, ['mtproto'], true, HOST, t)).toEqual({
      disabled: true,
      notice: 'limitIpUnsupportedKinds(mtproto)',
    });
  });

  it('a client on MTProto and SSH stays open and names the half a cap cannot reach', () => {
    expect(ipLimitFieldState(cores, ['mtproto', 'ssh'], true, HOST, t)).toEqual({
      disabled: false,
      notice: 'limitIpUnsupportedKinds(mtproto) limitIpEnforcedAtLogin(ssh)',
    });
  });
});
