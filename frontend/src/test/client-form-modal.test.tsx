import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import { renderWithProviders } from './test-utils';

/*
Both fields are shown only for an inbound whose core declares them, so the
tooltips are reached through a Trojan and a Hysteria inbound rather than through
an empty form. The manifest is what GET /panel/api/cores serves.
*/
const INBOUNDS = [
  { id: 1, port: 443, protocol: 'trojan', tag: 'in-443', enable: true },
  { id: 2, port: 8443, protocol: 'hysteria', tag: 'in-8443', enable: true },
] as unknown as InboundOption[];

const CLIENT = { email: 'testuser', enable: true } as unknown as ClientRecord;

function serveCores() {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    const obj = url === '/panel/api/cores'
      ? [{
        id: 'xray',
        titleKey: 'cores.xray.title',
        kinds: ['trojan', 'hysteria'],
        caps: {},
        clientCredentials: { trojan: ['password'], hysteria: ['auth'] },
      }]
      : {};
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    return { success: true, obj } as any;
  });
}

// ClientFormModal reads server state via react-query (useFail2banStatusQuery),
// so it needs a QueryClientProvider on top of the shared ThemeProvider wrapper.
function renderModal() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  renderWithProviders(
    <QueryClientProvider client={queryClient}>
      <ClientFormModal
        open
        mode="edit"
        client={CLIENT}
        inbounds={INBOUNDS}
        attachedIds={[1, 2]}
        save={vi.fn().mockResolvedValue(null)}
        onOpenChange={() => {}}
      />
    </QueryClientProvider>,
  );
}

function openCredentialsTab() {
  const tab = Array.from(document.querySelectorAll('.ant-tabs-tab'))
    .find((t) => (t.textContent ?? '').trim() === 'Credentials');
  if (!tab) throw new Error('Credentials tab not found');
  fireEvent.click(tab);
}

async function tooltipIconForLabel(label: string): Promise<HTMLElement> {
  let item: HTMLElement | null = null;
  await waitFor(() => {
    const labelEl = Array.from(document.querySelectorAll('.ant-form-item-label label'))
      .find((l) => (l.textContent ?? '').trim() === label);
    item = labelEl?.closest('.ant-form-item') as HTMLElement | null;
    if (!item) throw new Error(`Form item not found for label: ${label}`);
  });
  const tip = item!.querySelector('.ant-form-item-tooltip') as HTMLElement | null;
  if (!tip) throw new Error(`No tooltip on form item: ${label}`);
  return tip;
}

describe('ClientFormModal credential tooltips', () => {
  afterEach(() => {
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, obj: {} } as any);
  });

  it('explains that the Password field is only consumed by Trojan/Shadowsocks', async () => {
    serveCores();
    renderModal();
    openCredentialsTab();

    const tip = await tooltipIconForLabel('Password');
    fireEvent.mouseEnter(tip);

    await waitFor(() => {
      expect(document.body.textContent).toContain(
        'Only used by Trojan and Shadowsocks clients; ignored for VLESS, VMess, Hysteria, and WireGuard.',
      );
    });
  });

  it('explains that Hysteria Auth is the credential Hysteria actually uses', async () => {
    serveCores();
    renderModal();
    openCredentialsTab();

    const tip = await tooltipIconForLabel('Hysteria Auth');
    fireEvent.mouseEnter(tip);

    await waitFor(() => {
      expect(document.body.textContent).toContain(
        'Credential used only by Hysteria clients. Trojan and Shadowsocks use the Password field instead.',
      );
    });
  });
});
