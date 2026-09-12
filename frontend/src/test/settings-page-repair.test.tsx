import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import i18next from 'i18next';
import { AllSetting } from '@/models/setting';
import { AllSettingSchema } from '@/schemas/setting';
import SettingsPage from '@/pages/settings/SettingsPage';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

const text = (key: string, values?: Record<string, unknown>) => String(i18next.t(key, values));
const initialWidth = window.innerWidth;
afterEach(() => { Object.defineProperty(window, 'innerWidth', { configurable: true, value: initialWidth }); });

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="settings-location">{location.pathname}{location.search}{location.hash}</output>;
}

function setup({ hash = 'subscription', failRead = false, failSave = false, mobile = false } = {}) {
  if (mobile) Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
  let persisted = new AllSetting({ subPort: 2096, subDomain: 'sub.example.test', subJsonEnable: true, subClashEnable: true,
    subRoutingRules: 'happ://routing/add/preserved', subIncyRoutingRules: 'incy://routing/onadd/preserved', subClashRules: 'MATCH,DIRECT', subTitle: 'Original profile' });
  let rejectRead = failRead;
  let rejectSave = failSave;
  const save = vi.fn();
  vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url, payload) => {
    if (url === '/panel/api/setting/all') {
      if (rejectRead) { rejectRead = false; return new Msg(false, 'Settings read unavailable'); }
      return new Msg(true, '', { ...persisted });
    }
    if (url === '/panel/api/setting/update') {
      save(payload);
      if (rejectSave) { rejectSave = false; return new Msg(false, 'Settings write rejected'); }
      persisted = new AllSetting(AllSettingSchema.parse(payload));
      return new Msg(true, '');
    }
    if (url === '/panel/api/xray/') return new Msg(true, '', JSON.stringify({ xraySetting: { outbounds: [], routing: { balancers: [] } } }));
    return new Msg(true, '', {});
  });
  const view = renderWithProviders(<MemoryRouter initialEntries={[`/settings?retain=yes#${hash}`]}><SettingsPage /><LocationProbe /></MemoryRouter>);
  return { ...view, save };
}

function pick(label: string) {
  const rail = screen.getByRole('complementary');
  const button = within(rail).getAllByRole('button').find((element) => element.querySelector('.p-navicon') && element.textContent?.startsWith(label));
  expect(button).toBeDefined();
  fireEvent.click(button!);
}

describe('settings page repair', () => {
  it('keeps the heading first and makes read errors retryable', async () => {
    const view = setup({ failRead: true });
    await screen.findByText('Settings read unavailable');
    expect(view.container.querySelector('.settings-page')?.firstElementChild?.contains(screen.getByRole('heading', { level: 1 }))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: text('refresh') }));
    expect(await screen.findByDisplayValue('2096')).toBeTruthy();
  });

  it('retains failed edits across section navigation and resubmits all subscription settings', async () => {
    const view = setup({ failSave: true });
    const domain = await screen.findByDisplayValue('sub.example.test');
    const save = screen.getByRole('button', { name: text('pages.settings.save') });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(domain, { target: { value: 'changed.example.test' } });
    fireEvent.click(save);
    await screen.findByText('Settings write rejected');
    expect(screen.getByText(text('pages.settings.unsaved'))).toBeTruthy();
    pick(text('pages.settings.TGBotSettings'));
    expect(screen.getByTestId('settings-location').textContent).toBe('/settings?retain=yes#telegram');
    pick(text('pages.settings.subSettings'));
    expect(screen.getByDisplayValue('changed.example.test')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: text('pages.settings.save') }));
    await waitFor(() => expect(view.save).toHaveBeenCalledTimes(2));
    expect(view.save.mock.lastCall?.[0]).toMatchObject({ subDomain: 'changed.example.test', subPort: 2096, subRoutingRules: 'happ://routing/add/preserved', subIncyRoutingRules: 'incy://routing/onadd/preserved', subClashRules: 'MATCH,DIRECT', subTitle: 'Original profile' });
    await waitFor(() => expect(view.container.querySelector<HTMLButtonElement>('.settings-actions .ant-btn-primary')?.disabled).toBe(true));
    expect(screen.queryByText('Settings write rejected')).toBeNull();
  });

  it('offers restart only after a successful save changes a restart-only field', async () => {
    setup();
    const port = await screen.findByDisplayValue('2096');
    expect((screen.getByRole('button', { name: text('pages.settings.restartPanel') }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(port, { target: { value: '8443' } });
    expect((screen.getByRole('button', { name: text('pages.settings.restartPanel') }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: text('pages.settings.save') }));
    await waitFor(() => expect((screen.getByRole('button', { name: text('uiRepair.settings.restartApply', { count: 1 }) }) as HTMLButtonElement).disabled).toBe(false));
    expect(screen.getAllByText(new RegExp(text('pages.settings.subPort'))).length).toBeGreaterThan(0);
  });

  it('keeps warnings compact until expanded and supports dismissal', async () => {
    const view = setup();
    await screen.findByDisplayValue('2096');
    expect(screen.queryByText(text('pages.settings.panelExposed'))).toBeNull();
    const details = screen.getByRole('button', { name: text('uiRepair.settings.showDetails') });
    expect(details.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(details);
    expect(screen.getByText(text('pages.settings.panelExposed'))).toBeTruthy();
    const close = view.container.querySelector<HTMLButtonElement>('.settings-warning .ant-alert-close-icon');
    expect(close).not.toBeNull();
    fireEvent.click(close!);
    await waitFor(() => expect(view.container.querySelector('.settings-warning')).toBeNull());
  });

  it('keeps advanced groups collapsed and saves edits made before collapsing them', async () => {
    const view = setup();
    await screen.findByDisplayValue('2096');
    const groups = view.container.querySelectorAll('details');
    expect(groups).toHaveLength(5);
    for (const group of groups) expect(group.open).toBe(false);
    const profile = screen.getByText(text('pages.settings.profile'), { selector: 'h3' }).closest('summary')!;
    fireEvent.click(profile);
    expect(profile.closest('details')?.open).toBe(true);
    fireEvent.change(screen.getByRole('textbox', { name: text('pages.settings.subTitle') }), { target: { value: 'Edited profile' } });
    fireEvent.click(profile);
    expect(profile.closest('details')?.open).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: text('pages.settings.save') }));
    await waitFor(() => expect(view.save).toHaveBeenCalledWith(expect.objectContaining({ subTitle: 'Edited profile' })));
    fireEvent.click(profile);
    expect(screen.getByDisplayValue('Edited profile')).toBeTruthy();
  });

  it('keeps mobile fields visible with a collapsible searchable rail', async () => {
    setup({ mobile: true });
    expect(await screen.findByDisplayValue('2096')).toBeTruthy();
    expect(screen.queryByRole('textbox', { name: text('pages.settings.findSetting') })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: new RegExp(text('uiRepair.settings.sections')) }));
    const search = screen.getByRole('textbox', { name: text('pages.settings.findSetting') });
    fireEvent.change(search, { target: { value: 'LDAP' } });
    pick(text('design.ldap'));
    expect(screen.getByTestId('settings-location').textContent).toBe('/settings?retain=yes#ldap');
    expect(screen.queryByRole('textbox', { name: text('pages.settings.findSetting') })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: new RegExp(text('uiRepair.settings.sections')) }));
    expect((screen.getByRole('textbox', { name: text('pages.settings.findSetting') }) as HTMLInputElement).value).toBe('LDAP');
  });

  it.each(['general', 'notifications', 'certs', 'ext', 'time', 'ldap', 'iplimit', 'security', 'tokens', 'telegram', 'email', 'subscription', 'subscription-formats'])('contains %s controls in named cards', async (hash) => {
    const view = setup({ hash });
    await waitFor(() => expect(view.container.querySelector('.settings-content')).not.toBeNull());
    const groups = view.container.querySelectorAll('.settings-content .settings-group');
    expect(groups.length).toBeGreaterThan(0);
    for (const row of view.container.querySelectorAll('.settings-content .setting-list-item')) expect(row.closest('.settings-group')).not.toBeNull();
    for (const group of groups) expect(group.querySelector('h3')?.textContent).toBeTruthy();
  });
});
