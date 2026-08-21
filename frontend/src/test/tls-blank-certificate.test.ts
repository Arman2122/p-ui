import { describe, expect, it } from 'vitest';

import { createTlsSettingsWithDefaultCert } from '@/lib/xray/inbound-tls-defaults';
import { TlsCertSchema } from '@/schemas/protocols/security/tls';
import { InboundFormSchema } from '@/schemas/forms/inbound-form';
import { TcpStreamSettingsSchema } from '@/schemas/protocols/stream/tcp';

/*
Switching the security radio to TLS seeds one certificate with every field
empty, and nothing makes the operator fill it in. xray-core answers that with
"both file and bytes are empty.", which fails the whole config — every inbound
on the box stops on the next restart, with the save reported as a success.

The server refuses it now, but the modal should say so before the round trip,
and the union is what let it through: the file branch fails on the empty path
and the inline branch used to accept two empty arrays.
*/
describe('a blank TLS certificate', () => {
  it('is what the TLS radio seeds', () => {
    const tls = createTlsSettingsWithDefaultCert();
    const certificates = tls.certificates as Record<string, unknown>[];
    expect(certificates).toHaveLength(1);
    expect(certificates[0]).toMatchObject({ certificateFile: '', keyFile: '', certificate: [], key: [] });
  });

  it('does not parse as a certificate', () => {
    expect(TlsCertSchema.safeParse({ certificateFile: '', keyFile: '', certificate: [], key: [] }).success)
      .toBe(false);
  });

  it('does not reach the wire through the inbound form', () => {
    /* A certificate the operator did fill in is the control: without it the
       parse could be failing on some unrelated missing branch and prove nothing. */
    const filled = createTlsSettingsWithDefaultCert();
    (filled.certificates as Record<string, unknown>[])[0] = {
      certificateFile: '/etc/ssl/x.pem',
      keyFile: '/etc/ssl/x.key',
    };
    const formValues = (tlsSettings: Record<string, unknown>) => ({
      protocol: 'vless',
      port: 8443,
      settings: { clients: [], decryption: 'none' },
      streamSettings: {
        network: 'tcp',
        security: 'tls',
        tcpSettings: TcpStreamSettingsSchema.parse({}),
        tlsSettings,
      },
    });

    expect(InboundFormSchema.safeParse(formValues(createTlsSettingsWithDefaultCert())).success).toBe(false);
    expect(InboundFormSchema.safeParse(formValues(filled)).success).toBe(true);
  });

  it('still accepts a certificate the operator actually filled in', () => {
    expect(TlsCertSchema.safeParse({ certificateFile: '/etc/ssl/x.pem', keyFile: '/etc/ssl/x.key' }).success)
      .toBe(true);
    expect(TlsCertSchema.safeParse({ certificate: ['-----BEGIN CERTIFICATE-----'], key: ['-----BEGIN KEY-----'] }).success)
      .toBe(true);
  });
});
