import type { ComponentProps } from 'react';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import OutboundsTab from '@/pages/xray/outbounds/OutboundsTab';
import { exitAsOutboundRow, observedTraffic, referencedRules } from '@/pages/xray/outbounds/outbounds-tab-helpers';
import { PageHeader } from '@/components/ui/panel';
import type { EgressRecord } from '@/schemas/api/egress';
import type { RoutingRuleRecord } from '@/schemas/api/routing';
import type { XraySettingsValue } from '@/hooks/useXraySetting';
import { HttpUtil, Msg } from '@/utils';
import en from '../../../internal/web/translation/en-US.json';
import { renderWithProviders } from './test-utils';

const exit: EgressRecord = {
  id: 4, type: 'wg-client', enable: true, remark: 'kernel-exit', target: '', owner: 'operator',
  ingressInboundId: null, settings: JSON.stringify({ endpoint: 'gateway.example:51820', privateKey: 'kept', publicKey: 'kept-public', address: ['10.0.0.2/32'] }), createdAt: 0, updatedAt: 0,
};
const template = {
  outbounds: [{ tag: 'direct', protocol: 'freedom' }, { tag: 'proxy-a', protocol: 'socks', settings: { servers: [{ address: 'proxy.example', port: 443 }] } }],
  routing: { rules: [{ type: 'field', outboundTag: 'proxy-a', ruleTag: 'video' }] },
} as XraySettingsValue;

function renderTab(overrides: Partial<ComponentProps<typeof OutboundsTab>> = {}) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url) => new Msg(true, '',
    url.includes('/egresses/list') ? [exit] : url.includes('/egresses/preflight') ? { ok: true, refusals: [], notes: [] } : []));
  const props = {
    templateSettings: template, setTemplateSettings: vi.fn(), outboundsTraffic: [], outboundTestStates: {}, subscriptionTestStates: {},
    testingAll: false, inboundTags: [], isMobile: false, onResetTraffic: vi.fn(), onTest: vi.fn(), onTestSubscription: vi.fn(),
    onTestAll: vi.fn(), onShowWarp: vi.fn(), onShowNord: vi.fn(), ...overrides,
  };
  renderWithProviders(<OutboundsTab {...props} />);
  return props;
}
function desktopRow(tag: string) {
  const row = [...document.querySelectorAll<HTMLElement>('tr.ant-table-row')].find((item) => item.textContent?.includes(tag));
  if (!row) throw new Error(`Missing row ${tag}`);
  return row;
}

afterEach(() => vi.restoreAllMocks());

describe('outbounds page composition and real controls', () => {
  it('places primary actions in the only page header and keeps the table boxed', async () => {
    const props = renderTab({ standalone: true, renderHeader: (actions) => <PageHeader title="Outbounds" actions={actions} /> });
    await screen.findByText('kernel-exit');
    expect(screen.getAllByRole('heading')).toHaveLength(1);
    const header = screen.getByRole('heading', { name: 'Outbounds' }).closest('.p-pagehead')!;
    fireEvent.click(within(header as HTMLElement).getByRole('button', { name: new RegExp(en.pages.xray.outbound.testAll) }));
    expect(props.onTestAll).toHaveBeenCalledWith('tcp', [1_000_004]);
    expect(document.querySelector('.p-card .outbounds-table')).not.toBeNull();
    expect(screen.getAllByRole('switch')).toHaveLength(1);
    expect((within(desktopRow('direct')).getByRole('button', { name: en.check }) as HTMLButtonElement).disabled).toBe(true);
    expect(within(desktopRow('proxy-a')).queryByRole('switch')).toBeNull();
    fireEvent.click(within(desktopRow('proxy-a')).getByRole('button', { name: en.check }));
    expect(props.onTest).toHaveBeenCalledWith(1, 'tcp');
  });

  it('embeds without another heading and protects built-in deletion', async () => {
    renderTab();
    await screen.findByText('kernel-exit');
    expect(screen.queryByRole('heading')).toBeNull();
    fireEvent.click(within(desktopRow('direct')).getByRole('button', { name: en.more }));
    expect((await screen.findByRole('menuitem', { name: new RegExp(en.delete) })).getAttribute('aria-disabled')).toBe('true');
  });

  it('does not invent host traffic from an identically named Xray counter', async () => {
    renderTab({ outboundsTraffic: [{ tag: 'kernel-exit', up: 500, down: 500 }, { tag: 'proxy-a', up: 0, down: 0 }] });
    await screen.findByText('kernel-exit');
    expect(desktopRow('kernel-exit').textContent).not.toContain('1000');
    expect(desktopRow('kernel-exit').querySelector('.outbound-traffic')).toBeNull();
    expect(desktopRow('proxy-a').querySelector('.outbound-traffic')?.textContent).toContain('0');
    expect(desktopRow('direct').querySelector('.outbound-traffic')).toBeNull();
  });

  it('keeps the host enabled when the server refuses disabling it', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'Host refused change'));
    const props = renderTab();
    const toggle = await screen.findByRole('switch');
    expect(toggle.getAttribute('aria-checked')).toBe('true');
    fireEvent.click(toggle);
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/egresses/update/4', expect.objectContaining({
      id: 4, type: 'wg-client', enable: false, settings: exit.settings, owner: exit.owner, ingressInboundId: null,
    }), expect.anything()));
    await screen.findByText('Host refused change');
    expect(toggle.getAttribute('aria-checked')).toBe('true');
    expect(props.setTemplateSettings).not.toHaveBeenCalled();
  });

  it('routes mobile host edit/delete through the host APIs rather than template positions', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, ''));
    const props = renderTab({ isMobile: true });
    const label = await screen.findByText('kernel-exit');
    const card = label.closest('.outbound-card') as HTMLElement;
    fireEvent.click(within(card).getByRole('button', { name: en.edit }));
    expect((await screen.findByRole('dialog')).textContent).toContain(en.pages.xray.egress.edit);
    expect(screen.getByDisplayValue('gateway.example:51820')).not.toBeNull();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: en.cancel }));
    fireEvent.click(within(card).getByRole('button', { name: en.more }));
    fireEvent.click(await screen.findByRole('menuitem', { name: new RegExp(en.delete) }));
    fireEvent.click(await screen.findByRole('button', { name: en.confirm }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/egresses/del/4'));
    expect(props.setTemplateSettings).not.toHaveBeenCalled();
  });

  it('reports a partially refused subscription refresh and still attempts the remaining subscriptions', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => new Msg(!url.includes('/7/'), ''));
    renderTab();
    await screen.findByText('kernel-exit');
    vi.mocked(HttpUtil.get).mockResolvedValue(new Msg(true, '', [{ id: 7, remark: 'first', enabled: true }, { id: 8, remark: 'second', enabled: true }]));
    const toolbar = document.querySelector('.outbounds-embedded-actions') as HTMLElement;
    fireEvent.click(within(toolbar).getByRole('button', { name: new RegExp(en.more) }));
    fireEvent.click(await screen.findByRole('menuitem', { name: new RegExp(en.pages.xray.outboundSub.manage) }));
    fireEvent.click(await screen.findByRole('button', { name: new RegExp(en.pages.xray.outboundSub.refreshAll) }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/xray/outbound-subs/8/refresh'));
    await waitFor(() => expect(document.querySelector('.ant-message-error')).not.toBeNull());
    expect(document.querySelector('.ant-message-success')).toBeNull();
    expect(post).toHaveBeenCalledWith('/panel/api/xray/outbound-subs/7/refresh');
  });

  it('counts direct, balancer-prefix and host references without claiming unloaded metadata is empty', () => {
    const settings = { ...template, routing: { ...template.routing, balancers: [{ tag: 'pool', selector: ['proxy-'] }], rules: [
      { type: 'field', outboundTag: 'proxy-a', ruleTag: 'video' }, { type: 'field', balancerTag: 'pool', ruleTag: 'pool-route' },
    ] } } as XraySettingsValue;
    const host = [{ id: 3, destKind: 'outbound', destTag: 'proxy-a', remark: 'host proxy' }, { id: 4, destKind: 'exit', destExitId: exit.id, remark: 'host exit' }] as RoutingRuleRecord[];
    const row = { tag: 'proxy-a', protocol: 'socks', key: 1 };
    expect(referencedRules(settings, row, undefined)).toBeUndefined();
    expect(referencedRules(settings, row, host)).toEqual(['video', 'pool-route', 'host proxy']);
    expect(referencedRules(settings, exitAsOutboundRow(exit), host)).toEqual(['host exit']);
    expect(observedTraffic([{ tag: exit.remark, up: 0, down: 50 }], exitAsOutboundRow(exit))).toBeUndefined();
  });
});





