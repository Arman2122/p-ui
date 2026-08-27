import { describe, it, expect, afterEach } from 'vitest';
import { cleanup, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ClientFormModal from '@/pages/clients/ClientFormModal';
import type { InboundOption } from '@/hooks/useClients';
import { renderWithProviders } from './test-utils';

const INBOUNDS = [
  { id: 1, remark: 'vless-in', protocol: 'vless', port: 443, enable: true },
] as unknown as InboundOption[];

function renderForm() {
  return renderWithProviders(
    <ClientFormModal
      open
      mode="add"
      client={null}
      inbounds={INBOUNDS}
      save={async () => null}
      onOpenChange={() => {}}
    />,
  );
}

afterEach(() => {
  cleanup();
});

/*
An Auto Renew period with no expiry date can never fire.

The field is labelled "Auto Renew (days)" and its tooltip says "Auto-renewal
after expiration", which is exactly what the panel does: autoRenewClients
selects on `reset > 0 and expiry_time > 0 and expiry_time <= now` and pushes
expiryTime forward. Quota exhaustion never reaches it -- depletedCondFrom has no
reset term.

So a client given a renew period and no expiry date has nothing to renew. It is
disabled the moment its quota runs out and stays that way, and DelDepletedClients
spares it too because that clause requires `reset = 0`. The form took the number
without a word, and the operator who typed it believed the client was covered.

F125.
*/
describe('the Auto Renew field with no expiry date', () => {
  it('says the period will never run', async () => {
    const user = userEvent.setup();
    renderForm();

    const renew = await screen.findByLabelText(/Auto Renew/i);
    await user.clear(renew);
    await user.type(renew, '30');

    expect(
      screen.queryByText(/never runs/i),
      'a renew period was accepted with no expiry date and nothing said so',
    ).not.toBeNull();
  });

  it('says nothing once an expiry is set', async () => {
    const user = userEvent.setup();
    renderForm();

    // "Start After First Use" is the expiry shape reachable without a date picker.
    const label = screen.getByText('Start After First Use');
    const toggle = label.closest('.ant-form-item')?.querySelector('.ant-switch');
    expect(toggle, 'the delayed-start switch was not found').not.toBeNull();
    await user.click(toggle as HTMLElement);

    const days = await screen.findByLabelText(/Duration \(days\)/i);
    await user.clear(days);
    await user.type(days, '30');

    const renew = await screen.findByLabelText(/Auto Renew/i);
    await user.clear(renew);
    await user.type(renew, '30');

    expect(screen.queryByText(/never runs/i)).toBeNull();
  });
});
