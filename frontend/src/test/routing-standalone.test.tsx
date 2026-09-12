import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Button, Space } from 'antd';
import i18next from 'i18next';
import RoutingTab from '@/pages/xray/routing/RoutingTab';
import { useRoutingDraft } from '@/hooks/useRoutingDraft';
import { XraySettingsValueSchema, type XraySettingsValue } from '@/schemas/xray';
import { RoutingRuleSchema, recordToPayload, type RoutingRuleRecord } from '@/schemas/api/routing';
import { HttpUtil, Msg } from '@/utils';
import { PageHeader } from '@/components/ui/panel';
import { renderWithProviders } from './test-utils';

const translate = (key: string) => new RegExp(i18next.t(key));
const record = (id: number, remark: string) => RoutingRuleSchema.parse({
  id, sortIndex: id - 1, enable: true, remark, ingressScope: 'selected', ingressIds: '[7]',
  destKind: 'outbound', destTag: 'direct', destExitId: null, criteria: JSON.stringify({ domain: [`${remark.toLowerCase()}.example`] }), inspect: false,
});
const initialTemplate = () => XraySettingsValueSchema.parse({
  routing: { domainStrategy: 'AsIs', rules: [
    { type: 'field', inboundTag: ['api'], outboundTag: 'api', enabled: true },
    { type: 'field', inboundTag: ['_bl_hidden'], outboundTag: 'direct', domain: ['hidden.example'], enabled: true },
    { type: 'field', domain: ['template.example'], outboundTag: 'direct', enabled: true },
    { type: 'field', ip: ['geoip:private'], outboundTag: 'blocked', enabled: false },
  ] },
  outbounds: [{ tag: 'direct', protocol: 'freedom' }, { tag: 'blocked', protocol: 'blackhole' }],
});

function Harness() {
  const routing = useRoutingDraft();
  const [template, setTemplate] = useState<XraySettingsValue | null>(initialTemplate);
  const templateDirty = JSON.stringify(template) !== JSON.stringify(initialTemplate());
  return <>
    <RoutingTab standalone templateSettings={template} setTemplateSettings={setTemplate} routing={routing}
      inboundTags={['edge']} clientReverseTags={[]} isMobile={false} hasUnsavedChanges={templateDirty}
      renderHeader={(actions) => <PageHeader title="Routing" actions={<Space>{actions}<Button disabled={!routing.dirty} onClick={() => void routing.flush()}>Save draft rules</Button></Space>} />} />
    <output aria-label="operator draft">{JSON.stringify(routing.rules)}</output>
    <output aria-label="template draft">{JSON.stringify(template?.routing?.rules)}</output>
  </>;
}

function mockServer() {
  let rules: RoutingRuleRecord[] = [record(1, 'Ads'), record(2, 'Media')];
  let nextId = 3;
  let readError = false;
  const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async (url) => {
    if (url.endsWith('/routing/rules')) return readError ? new Msg(false, 'Cannot load saved routing rules', null) : new Msg(true, '', rules);
    if (url.endsWith('/routing/subjects')) return new Msg(true, '', [{ inboundId: 7, tag: 'edge', selector: 'user', routable: true, criteriaMask: ['domain', 'ip', 'protocol', 'user'] }]);
    if (url.endsWith('/inbounds/options')) return new Msg(true, '', [{ id: 7, tag: 'edge', remark: 'Europe', protocol: 'vless', port: 443 }]);
    return new Msg(true, '', []);
  });
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (url, payload) => {
    if (url.endsWith('/routing/rules/order')) {
      const ids = (payload as { ids: number[] }).ids;
      rules = ids.map((id) => rules.find((row) => row.id === id)!);
    } else if (url.endsWith('/routing/rules')) {
      const row = RoutingRuleSchema.parse({ ...(payload as object), id: nextId++, sortIndex: rules.length });
      rules = [...rules, row];
      return new Msg(true, '', row);
    } else if (url.includes('/routing/rules/')) {
      const id = Number(url.split('/')[5]);
      rules = url.endsWith('/del') ? rules.filter((row) => row.id !== id) : rules.map((row) => row.id === id ? RoutingRuleSchema.parse({ ...row, ...(payload as object) }) : row);
    } else if (url.endsWith('/routeTest')) return new Msg(true, '', { matched: true, outboundTag: 'direct' });
    return new Msg(true, '', {});
  });
  return { get, post, setReadError: (value: boolean) => { readError = value; } };
}

function draft(): RoutingRuleRecord[] {
  return JSON.parse(screen.getByLabelText('operator draft').textContent || '[]') as RoutingRuleRecord[];
}

async function renderPage() {
  renderWithProviders(<Harness />);
  await waitFor(() => expect(draft()).toHaveLength(2));
}

afterEach(() => vi.restoreAllMocks());

describe('standalone routing page', () => {
  it('shows a failed rules load and retries before enabling draft edits', async () => {
    const server = mockServer();
    server.setReadError(true);
    renderWithProviders(<Harness />);
    expect(await screen.findByText('Cannot load saved routing rules')).toBeTruthy();
    expect((screen.getByRole('button', { name: translate('routingPage.addRule') }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.queryByText(i18next.t('routingPage.empty'))).toBeNull();
    expect(server.post).not.toHaveBeenCalled();
    server.setReadError(false);
    fireEvent.click(screen.getByRole('button', { name: translate('routingPage.retry') }));
    await waitFor(() => expect(draft()).toHaveLength(2));
    expect((screen.getByRole('button', { name: translate('routingPage.addRule') }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('opens the overview with primary actions and drafts real toggle, duplicate and save operations', async () => {
    const { post } = mockServer();
    await renderPage();
    expect(screen.getByRole('heading', { name: 'Routing' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: translate('routingPage.overview') }).getAttribute('aria-selected')).toBe('true');
    expect(document.querySelectorAll('.routing-overview-row')).toHaveLength(5);
    expect(document.querySelector('.routing-settings-panel')).toBeNull();
    fireEvent.click(screen.getByRole('switch', { name: `${i18next.t('enable')} Ads` }));
    fireEvent.click(screen.getByRole('button', { name: `${i18next.t('routingPage.duplicate')} Media` }));
    expect(post).not.toHaveBeenCalled();
    expect(draft()).toHaveLength(3);
    expect(recordToPayload(draft()[2])).toEqual(recordToPayload(record(2, 'Media')));
    fireEvent.click(screen.getByRole('button', { name: 'Save draft rules' }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/routing/rules/1', { ...recordToPayload(record(1, 'Ads')), enable: false }, expect.anything()));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/routing/rules', recordToPayload(record(2, 'Media')), expect.anything()));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/routing/rules/order', { ids: [1, 2, 3] }, expect.anything()));
  });

  it('creates and edits through the real rule form without writing before save', async () => {
    const { post } = mockServer();
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: translate('routingPage.addRule') }));
    fireEvent.change(await screen.findByRole('textbox', { name: translate('domainName') }), { target: { value: 'new.example' } });
    fireEvent.click(screen.getByRole('button', { name: new RegExp(`^${i18next.t('create')}$`) }));
    await waitFor(() => expect(draft()).toHaveLength(3));
    fireEvent.click(screen.getByRole('button', { name: `${i18next.t('edit')} Ads` }));
    fireEvent.change(await screen.findByRole('textbox', { name: translate('domainName') }), { target: { value: 'changed.example' } });
    fireEvent.click(screen.getByRole('button', { name: new RegExp(`^${i18next.t('pages.clients.submitEdit')}$`) }));
    await waitFor(() => expect(JSON.parse(String(draft()[0].criteria))).toMatchObject({ domain: ['changed.example'] }));
    expect(post).not.toHaveBeenCalled();
    expect(draft()[0].ingressIds).toBe('[7]');
  });

  it('keeps ordering within each source and protects the fixed API rule', async () => {
    const { post } = mockServer();
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: translate('routingPage.reorder') }));
    const down = screen.getByRole('button', { name: `${i18next.t('pages.inbounds.form.moveDown')} Media` }) as HTMLButtonElement;
    expect(down.disabled).toBe(true);
    const apiRow = Array.from(document.querySelectorAll('.routing-overview-row')).find((row) => row.textContent?.includes('api'))!;
    expect((within(apiRow as HTMLElement).getByRole('switch') as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: `${i18next.t('pages.inbounds.form.moveUp')} Media` }));
    expect(draft().map((row) => row.id)).toEqual([2, 1]);
    const templateRow = screen.getByText('template.example').closest('li')!;
    fireEvent.click(within(templateRow).getByRole('button', { name: translate('routingPage.duplicate') }));
    const template = JSON.parse(screen.getByLabelText('template draft').textContent || '[]') as Array<{ domain?: string[]; inboundTag?: string[] }>;
    expect(template[1].inboundTag).toEqual(['_bl_hidden']);
    expect(template[2].domain).toEqual(['template.example']);
    expect(template[3].domain).toEqual(['template.example']);
    expect(post).not.toHaveBeenCalled();
  });

  it('confirms deletion and only posts it on save', async () => {
    const { post } = mockServer();
    await renderPage();
    fireEvent.click(screen.getByRole('button', { name: `${i18next.t('delete')} Ads` }));
    expect(draft()).toHaveLength(2);
    fireEvent.click(await screen.findByRole('button', { name: new RegExp(`^${i18next.t('delete')}$`) }));
    await waitFor(() => expect(draft()).toHaveLength(1));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Save draft rules' }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/routing/rules/1/del'));
  });

  it('blocks saved-server route tests while the draft is dirty', async () => {
    const { post } = mockServer();
    await renderPage();
    fireEvent.click(screen.getByRole('switch', { name: `${i18next.t('enable')} Ads` }));
    fireEvent.click(screen.getByRole('tab', { name: translate('pages.xray.routeTester') }));
    fireEvent.change(await screen.findByRole('textbox', { name: translate('pages.xray.routeTesterDest') }), { target: { value: 'example.com' } });
    const test = screen.getByRole('button', { name: translate('pages.xray.routeTesterTest') }) as HTMLButtonElement;
    expect(test.disabled).toBe(true);
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Save draft rules' }));
    await waitFor(() => expect(test.disabled).toBe(false));
    fireEvent.click(test);
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/xray/routeTest', expect.objectContaining({ domain: 'example.com' })));
  });

  it('blocks the saved-server tester for unsaved template changes too', async () => {
    const { post } = mockServer();
    await renderPage();
    const templateRow = screen.getByText('template.example').closest('li')!;
    fireEvent.click(within(templateRow).getByRole('switch'));
    fireEvent.click(screen.getByRole('tab', { name: translate('pages.xray.routeTester') }));
    fireEvent.change(await screen.findByRole('textbox', { name: translate('pages.xray.routeTesterDest') }), { target: { value: 'example.com' } });
    expect((screen.getByRole('button', { name: translate('pages.xray.routeTesterTest') }) as HTMLButtonElement).disabled).toBe(true);
    expect(post).not.toHaveBeenCalled();
  });
});
