import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from '@testing-library/react';

import EgressFormModal from '@/pages/xray/egresses/EgressFormModal';
import { EGRESS_TYPE_UPLINK, EgressFormSchema } from '@/schemas/api/egress';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const UPLINK = {
  id: 4,
  type: EGRESS_TYPE_UPLINK,
  enable: true,
  remark: 'US-sfo | Surfshark',
  target: '',
  settings: JSON.stringify({
    privateKey: 'YJn/kRfVFgNyOAooWReN/D3+vFDzxuV/0+KHhl9nm2Y=',
    address: ['10.14.0.2/32', 'fd00::2/128'],
    mtu: 1420,
    publicKey: '7SpGSSI78hf8jy689ec5Ql0/Gsq0LLHDmjEFsGUWl1k=',
    endpoint: 'us-sfo.example.com:51820',
    presharedKey: '',
    keepAlive: 25,
  }),
  createdAt: 0,
  updatedAt: 0,
};

async function renderModal(egress: typeof UPLINK | null) {
  vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  renderWithProviders(<EgressFormModal open egress={egress} onClose={() => {}} />);
  for (let i = 0; i < 6; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

describe('the uplink half of the egress form', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  /* The provider's own fields, so an operator pasting a .conf sees each one it
     names. A missing field here is a tunnel that handshakes and carries nothing. */
  it('asks for what a provider actually hands out', async () => {
    await renderModal(UPLINK);

    const text = document.body.textContent ?? '';
    for (const label of ['Endpoint', 'Private key', 'Peer public key', 'Address']) {
      expect(text).toContain(label);
    }
    const endpoint = document.querySelector('#endpoint') as HTMLInputElement | null;
    expect(endpoint?.value).toBe('us-sfo.example.com:51820');
  });

  /* Editing an uplink must round-trip its settings. A form that reopened empty
     would quietly blank the keys on the next save. */
  it('reads an existing uplink back out of its settings', async () => {
    await renderModal(UPLINK);

    const address = document.querySelector('#address') as HTMLTextAreaElement | null;
    expect(address?.value).toBe('10.14.0.2/32\nfd00::2/128');
  });
});

describe('the uplink form when a required field is empty', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  /* A Save that neither saves nor explains is the worst of both. The refine
     blocks the submit; these messages are what tell the operator which field. */
  it('says which field is missing instead of doing nothing', async () => {
    const empty = { ...UPLINK, settings: '' };
    await renderModal(empty);

    const save = Array.from(document.querySelectorAll('button'))
      .find((b) => b.textContent?.trim() === 'Save');
    await act(async () => { save?.click(); });
    for (let i = 0; i < 4; i += 1) {
      await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
    }

    const text = document.body.textContent ?? '';
    expect(text).toContain(enUS.pages.xray.egress.endpointRequired);
    expect(text).toContain(enUS.pages.xray.egress.privateKeyRequired);
  });
});

describe('what the uplink form is allowed to submit', () => {
  /* An uplink IS the destination, so the backend rejects one that also names a
     target. The refusals are per-type, which is the whole reason for the refine. */
  it('requires the provider fields and no target', () => {
    const base = {
      id: 0, type: EGRESS_TYPE_UPLINK, remark: '', target: '', enable: true,
      privateKey: '', address: '', mtu: 0, publicKey: '', endpoint: '',
      presharedKey: '', keepAlive: 0,
    };

    const empty = EgressFormSchema.safeParse(base);
    expect(empty.success).toBe(false);
    const paths = empty.success ? [] : empty.error.issues.map((i) => i.path[0]);
    expect(paths).toEqual(expect.arrayContaining(['privateKey', 'publicKey', 'endpoint', 'address']));

    const filled = EgressFormSchema.safeParse({
      ...base,
      privateKey: 'k', publicKey: 'p', endpoint: 'host:51820', address: '10.14.0.2/32',
    });
    expect(filled.success).toBe(true);
  });

  /* A front is the other shape: it needs the target an uplink must not have. */
  it('still requires a target for a front', () => {
    const front = EgressFormSchema.safeParse({
      id: 0, type: 'xray-tun', remark: '', target: '', enable: true,
      privateKey: '', address: '', mtu: 0, publicKey: '', endpoint: '',
      presharedKey: '', keepAlive: 0,
    });
    expect(front.success).toBe(false);
    const paths = front.success ? [] : front.error.issues.map((i) => i.path[0]);
    expect(paths).toContain('target');
  });
});
