import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, fireEvent, waitFor, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { ThemeProvider } from '@/hooks/useTheme';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';

function makeQC() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

const REALITY_INBOUND = {
  id: 4,
  port: 10443,
  protocol: 'vless',
  tag: 'in-10443-tcp',
  tlsFlowCapable: true,
  enable: true,
} as unknown as InboundOption;

const CLIENT = {
  email: 'testuser',
  flow: 'xtls-rprx-vision',
  uuid: '11111111-1111-1111-1111-111111111111',
  subId: 'subid123',
  enable: true,
} as unknown as ClientRecord;

function savedFlow(save: ReturnType<typeof vi.fn>): unknown {
  return (save.mock.calls[0][0] as Record<string, unknown>).flow;
}

describe('ClientFormModal — Vision flow preservation', () => {
  it('keeps xtls-rprx-vision with a stable Reality inbound', async () => {
    const qc = makeQC();
    const save = vi.fn().mockResolvedValue({ success: true });
    render(
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <ClientFormModal open mode="edit" client={CLIENT} inbounds={[REALITY_INBOUND]} attachedIds={[4]} save={save} onOpenChange={() => {}} />
        </QueryClientProvider>
      </ThemeProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(savedFlow(save)).toBe('xtls-rprx-vision');
  });

  it('does not drop a selected Vision flow while the inbound options momentarily reload', async () => {
    const qc = makeQC();
    const save = vi.fn().mockResolvedValue({ success: true });
    const tree = (inbounds: InboundOption[]) => (
      <ThemeProvider>
        <QueryClientProvider client={qc}>
          <ClientFormModal open mode="edit" client={CLIENT} inbounds={inbounds} attachedIds={[4]} save={save} onOpenChange={() => {}} />
        </QueryClientProvider>
      </ThemeProvider>
    );
    // Options loaded -> reloading (inboundOptionsQuery.data ?? [] === []) -> loaded again.
    const { rerender } = render(tree([REALITY_INBOUND]));
    await screen.findByRole('button', { name: /save/i });
    rerender(tree([]));
    rerender(tree([REALITY_INBOUND]));

    fireEvent.click(await screen.findByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(savedFlow(save)).toBe('xtls-rprx-vision');
  });
});

/* One core declaring vless and one declaring nothing, as GET /panel/api/cores serves them. */
function serveCores(clientCredentials: Record<string, string[]> | null) {
  const obj = clientCredentials === null
    ? []
    : [{ id: 'xray', titleKey: 'cores.xray.title', kinds: ['vless'], caps: {}, clientCredentials }];
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => (
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    (url === '/panel/api/cores' ? { success: true, obj } : { success: true, obj: {} }) as any
  ));
}

async function openCredentialsTab(inbounds: InboundOption[]) {
  const qc = makeQC();
  render(
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <ClientFormModal open mode="edit" client={CLIENT} inbounds={inbounds} attachedIds={[4]} save={vi.fn()} onOpenChange={() => {}} />
      </QueryClientProvider>
    </ThemeProvider>,
  );
  fireEvent.click(await screen.findByRole('tab', { name: 'Credentials' }));
  /* Every client has a subscription id, so it marks the tab as rendered. */
  await screen.findByText('Subscription ID');
}

describe('ClientFormModal — credential fields come from what the core declares', () => {
  afterEach(() => {
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, obj: {} } as any);
  });

  it('shows only the fields vless declares, not password and Hysteria auth', async () => {
    serveCores({ vless: ['uuid'] });
    await openCredentialsTab([REALITY_INBOUND]);

    expect(screen.getByText('UUID')).toBeTruthy();
    expect(screen.queryByText('Password')).toBeNull();
    expect(screen.queryByText('Hysteria Auth')).toBeNull();
  });

  it('keeps every field the form has always shown when no core declares the kind', async () => {
    serveCores(null);
    await openCredentialsTab([REALITY_INBOUND]);

    expect(screen.getByText('UUID')).toBeTruthy();
    expect(screen.getByText('Password')).toBeTruthy();
    expect(screen.getByText('Hysteria Auth')).toBeTruthy();
  });
});
