import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from '@testing-library/react';

import RoutingRulesPanel from '@/pages/xray/routing/RoutingRulesPanel';
import { criteriaFromForm, criteriaToForm, ingressIdsToArray } from '@/schemas/api/routing';
import { HttpUtil, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';

import { renderWithProviders } from './test-utils';

const SUBJECTS = [
  { inboundId: 1, tag: 'vless-in', selector: 'internal', routable: true, blockedKey: '', criteriaMask: ['ip', 'port', 'domain', 'user'] },
  { inboundId: 2, tag: 'wg-home', selector: 'device', routable: true, blockedKey: '', criteriaMask: ['ip', 'port'] },
  { inboundId: 3, tag: 'mt-1', selector: 'internal', routable: false, blockedKey: 'pages.xray.subjects.reasonBridgeOff', criteriaMask: [] },
];

const RULES = [
  { id: 1, sortIndex: 0, enable: true, remark: '', ingressScope: 'selected', ingressIds: '[2]', destKind: 'outbound', destTag: 'warp', destExitId: null, criteria: '{}', inspect: false },
  { id: 2, sortIndex: 1, enable: true, remark: '', ingressScope: 'selected', ingressIds: '[1]', destKind: 'block', destTag: '', destExitId: null, criteria: '{"domain":["geosite:ads"]}', inspect: false },
];

/*
Criteria are stored as JSON in a column and edited as comma-separated text, so
the round trip is where a rule quietly loses a matcher.
*/
describe('routing criteria round trip', () => {
  it('splits a list criterion and drops the empties', () => {
    const body = criteriaFromForm({ domain: 'a.com, b.com ', port: '443', ip: '' });

    expect(JSON.parse(body)).toEqual({ domain: ['a.com', 'b.com'], port: '443' });
  });

  it('renders a stored list back as text', () => {
    expect(criteriaToForm('{"domain":["a.com","b.com"],"port":"443"}')).toEqual({
      domain: 'a.com,b.com',
      port: '443',
    });
  });

  it('survives unparsable stored criteria rather than throwing', () => {
    expect(criteriaToForm('{not json')).toEqual({});
    expect(ingressIdsToArray('{not json')).toEqual([]);
  });

  it('reads ingress ids from either shape', () => {
    expect(ingressIdsToArray('[1,2]')).toEqual([1, 2]);
    expect(ingressIdsToArray([3])).toEqual([3]);
  });
});

async function renderPanel() {
  vi.spyOn(HttpUtil, 'get').mockImplementation((url: string) => {
    if (url.includes('/routing/rules')) return Promise.resolve(new Msg(true, '', RULES));
    if (url.includes('/routing/subjects')) return Promise.resolve(new Msg(true, '', SUBJECTS));
    return Promise.resolve(new Msg(true, '', []));
  });
  renderWithProviders(
    <RoutingRulesPanel isMobile={false} outboundTags={['warp']} balancerTags={[]} />,
  );
  for (let i = 0; i < 8; i += 1) {
    await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  }
}

describe('the routing rules panel', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  /* One table, not two: intent rules render through the same columns the
     template rules use, so a rule reads the same whichever half carries it. */
  it('renders each rule in the shared table, naming its inbound and destination', async () => {
    await renderPanel();

    const text = document.body.textContent ?? '';
    expect(text).toContain('wg-home');
    expect(text).toContain('warp');
  });

  it('says which inbounds no rule can name, and why', async () => {
    await renderPanel();

    const text = document.body.textContent ?? '';
    expect(text).toContain('mt-1');
    expect(text).toContain(enUS.pages.xray.subjects.reasonBridgeOff.slice(0, 40));
  });

  /* A kernel inbound is unroutable in the TEMPLATE's tag list and routable
     here, because this is the half that fronts it. Badging it "never matches"
     told the operator the opposite of the truth. */
  it('does not brand a fronted kernel inbound as never matching', async () => {
    await renderPanel();

    expect(document.body.textContent).not.toContain(enUS.pages.xray.subjects.neverMatches);
  });

  it('says what the rules are evaluated against', async () => {
    await renderPanel();

    expect(document.body.textContent).toContain(enUS.pages.xray.routing.intentHint);
  });
});
