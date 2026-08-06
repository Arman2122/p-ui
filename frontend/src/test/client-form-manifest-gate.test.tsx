import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';

import { HttpUtil, Msg } from '@/utils';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import { renderWithProviders } from './test-utils';

/*
GET /panel/api/cores starts when the modal mounts, so everything derived from it
is undefined on first render. The form must not guess: it once rendered
uuid/password/auth for an MTProto client and then saved without secret or adTag,
and an empty adTag reads server-side as an explicit clear.
*/
const MTPROTO_INBOUND = {
  id: 7,
  port: 8443,
  protocol: 'mtproto',
  tag: 'in-8443',
  mtprotoDomain: 'www.cloudflare.com',
  enable: true,
} as unknown as InboundOption;

const AD_TAG = '0123456789abcdef0123456789abcdef';

const CLIENT = {
  email: 'mtuser',
  subId: 'subid123',
  secret: 'ee0123456789abcdef0123456789abcdef7777772e636c6f7564666c6172652e636f6d',
  adTag: AD_TAG,
  enable: true,
} as unknown as ClientRecord;

const MANIFEST = [{
  id: 'mtproto',
  titleKey: 'cores.mtproto.title',
  kinds: ['mtproto'],
  caps: {},
  clientCredentials: { mtproto: ['secret', 'adTag'] },
}];

/* Holds /panel/api/cores open so the window between mount and manifest is a
   state the test can assert on rather than a race it has to win. */
function deferCores() {
  let settle: (msg: Msg<unknown>) => void = () => {};
  const pending = new Promise<Msg<unknown>>((resolve) => { settle = resolve; });
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => (
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    (url === '/panel/api/cores' ? pending : Promise.resolve(new Msg(true, '', {}))) as any
  ));
  /* Settled inside act so the manifest lands on a rendered tree, not between
     one — React 19 stalls the next query when it arrives outside. */
  return {
    serve: async () => { await act(async () => { settle(new Msg(true, '', MANIFEST)); }); },
  };
}

function failCores() {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => (
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    Promise.resolve(url === '/panel/api/cores' ? new Msg(false, 'cores unavailable') : new Msg(true, '', {})) as any
  ));
}

function renderModal() {
  const save = vi.fn().mockResolvedValue({ success: true });
  renderWithProviders(
    <ClientFormModal
      open
      mode="edit"
      client={CLIENT}
      inbounds={[MTPROTO_INBOUND]}
      attachedIds={[7]}
      save={save}
      onOpenChange={() => {}}
    />,
  );
  return save;
}

async function openCredentialsTab() {
  fireEvent.click(await screen.findByRole('tab', { name: 'Credentials' }));
}

describe('ClientFormModal — the credential fields wait for the manifest', () => {
  afterEach(() => {
    /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
    vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, obj: {} } as any);
  });

  it('offers no fields and no Save until the manifest says what MTProto stores', async () => {
    const cores = deferCores();
    const save = renderModal();
    await openCredentialsTab();

    expect(screen.queryByText('UUID')).toBeNull();
    expect(screen.queryByText('Password')).toBeNull();
    expect(screen.queryByRole('button', { name: /save/i })).toBeNull();

    await cores.serve();
    await screen.findByText('MTProto secret');
    expect(screen.queryByText('UUID')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /save/i }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect((save.mock.calls[0][0] as Record<string, unknown>).adTag).toBe(AD_TAG);
  });

  it('says the manifest failed instead of saving a client against a guess', async () => {
    failCores();
    const save = renderModal();
    await openCredentialsTab();

    await screen.findByText(/Could not load which credentials this build supports/);
    expect(screen.queryByRole('button', { name: /save/i })).toBeNull();
    expect(screen.getByText('Refresh')).toBeTruthy();
    expect(save).not.toHaveBeenCalled();
  });
});
