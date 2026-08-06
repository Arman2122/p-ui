import { describe, it, expect } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { FormProvider, useForm } from 'react-hook-form';

import { renderWithProviders } from './test-utils';
import XhttpForm from '@/pages/inbounds/form/transport/xhttp';

/*
"Copy from upload side" is the whole ergonomics of the two-addresses-on-one-
server case: the operator should only have to type the second address.

It is worth a test because getting it wrong is silent — the box fills in, looks
complete, and the download half fails later. REALITY is the trap: only
serverNames and shortIds sit at the top of realitySettings, while the
client-facing publicKey, spiderX and fingerprint live under .settings.
*/

let captured: Record<string, unknown> = {};

function Harness({ defaultValues, children }: { defaultValues: Record<string, unknown>; children: ReactNode }) {
  const methods = useForm({ defaultValues });
  captured = methods.watch() as Record<string, unknown>;
  return <FormProvider {...methods}>{children}</FormProvider>;
}

function download(): Record<string, unknown> {
  const stream = captured.streamSettings as Record<string, unknown> | undefined;
  const xhttp = stream?.xhttpSettings as Record<string, unknown> | undefined;
  return (xhttp?.downloadSettings ?? {}) as Record<string, unknown>;
}

describe('copy from upload side', () => {
  it('takes REALITY credentials from where the inbound actually stores them', () => {
    renderWithProviders(
      <Harness
        defaultValues={{
          streamSettings: {
            security: 'reality',
            realitySettings: {
              serverNames: ['front.example.com'],
              shortIds: ['abcd'],
              settings: { publicKey: 'PUBKEY', spiderX: '/x', fingerprint: 'firefox' },
            },
            xhttpSettings: { path: '/up', mode: 'stream-up', enableDownloadSettings: true },
          },
        }}
      >
        <XhttpForm />
      </Harness>,
    );

    fireEvent.click(screen.getByText('Copy from upload side'));

    const reality = download().realitySettings as Record<string, unknown>;
    expect(reality.publicKey).toBe('PUBKEY');
    expect(reality.spiderX).toBe('/x');
    expect(reality.fingerprint).toBe('firefox');
    expect(reality.serverName).toBe('front.example.com');
    expect(reality.shortId).toBe('abcd');
  });

  it('takes TLS credentials and the upload path', () => {
    renderWithProviders(
      <Harness
        defaultValues={{
          streamSettings: {
            security: 'tls',
            tlsSettings: { serverName: 'sni.example.com', alpn: ['h2'], fingerprint: 'chrome' },
            xhttpSettings: { path: '/up', mode: 'stream-up', enableDownloadSettings: true },
          },
        }}
      >
        <XhttpForm />
      </Harness>,
    );

    fireEvent.click(screen.getByText('Copy from upload side'));

    const tls = download().tlsSettings as Record<string, unknown>;
    expect(tls.serverName).toBe('sni.example.com');
    expect(tls.alpn).toEqual(['h2']);
    expect(tls.fingerprint).toBe('chrome');
    const inner = download().xhttpSettings as Record<string, unknown>;
    expect(inner.path).toBe('/up');
  });
});
