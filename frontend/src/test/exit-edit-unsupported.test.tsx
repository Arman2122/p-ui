import { describe, it, expect, afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

import ExitFormModal from '@/pages/xray/outbounds/ExitFormModal';
import type { EgressRecord } from '@/schemas/api/egress';
import { renderWithProviders } from './test-utils';

afterEach(() => {
  cleanup();
});

/*
An exit type the editor has no fields for must refuse, not pretend.

ExitFormModal resolves its field module from the row's stored type and rendered
`{module && <module.Fields />}`. Exactly one type has a module. Opening any
other — a panel-owned `xray-tun` front, or an `awg-client` uplink — gave a form
with nothing in it but the remark box, and an OK that ran a save writing nothing
and closed as though it had worked. Nothing told the operator the row was not
editable here.

OK is disabled now and the modal says why. F196.
*/

function exitRow(type: string): EgressRecord {
  return {
    id: 7,
    type,
    enable: true,
    owner: 'operator',
    remark: 'de-uplink',
  } as unknown as EgressRecord;
}

function okButton(): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll('.ant-modal-footer button'))
    .find((b) => (b.textContent ?? '').trim().length > 0 && b.classList.contains('ant-btn-primary')) as
    HTMLButtonElement | undefined;
}

describe('editing an exit the modal has no fields for', () => {
  for (const type of ['awg-client', 'xray-tun']) {
    it(`refuses ${type} visibly instead of saving nothing`, () => {
      renderWithProviders(<ExitFormModal open exit={exitRow(type)} onClose={() => {}} />);

      const ok = okButton();
      expect(ok, 'no primary button in the modal footer').toBeDefined();
      expect(ok?.disabled).toBe(true);

      // And it says so, rather than presenting an empty form.
      expect(document.querySelector('.ant-alert')).not.toBeNull();
    });
  }

  it('leaves OK enabled for the kind it can edit', () => {
    renderWithProviders(<ExitFormModal open exit={exitRow('wg-client')} onClose={() => {}} />);
    expect(okButton()?.disabled).toBe(false);
  });
});
