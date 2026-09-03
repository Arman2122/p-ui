import { describe, expect, it } from 'vitest';

import { uplinkPayload } from '@/schemas/api/egress';

/*
An uplink selects no ingress, and saying so with a zero made every create fail.

ingress_inbound_id carries a foreign key onto inbounds(id) and ids are
autoincrement, so no row is ever 0 — but a JSON scalar 0 binds to a non-nil
pointer on the server and reaches the column, where Postgres refuses it. This was
the panel's only path to authoring an uplink exit.
*/
describe('uplinkPayload', () => {
  const form = {
    id: 0,
    type: 'wg-client' as const,
    remark: '',
    target: '',
    enable: true,
    privateKey: 'cHJpdmF0ZUtleUZvclRlc3RpbmdQdXJwb3Nlc09ubHk=',
    address: '10.6.0.2/32',
    mtu: 1420,
    publicKey: 'cHVibGljS2V5Rm9yVGVzdGluZ1B1cnBvc2VzT25seUFB',
    endpoint: '198.51.100.7:51820',
    presharedKey: '',
    keepAlive: 25,
    sshHost: '',
    sshPort: 22,
    sshUser: '',
    sshPassword: '',
    sshKey: '',
    sshFingerprint: '',
  };

  it('selects no ingress rather than inbound zero', () => {
    const payload = uplinkPayload(form, 'provider');
    expect(payload.ingressInboundId).toBeNull();
    expect(payload.ingressInboundId).not.toBe(0);
  });
});
