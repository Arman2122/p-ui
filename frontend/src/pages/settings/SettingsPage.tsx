import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router';
import {
  Alert,
  Button,
  ConfigProvider,
  FloatButton,
  Modal,
  Result,
  Space,
  Spin,
  Tooltip,
  message,
} from 'antd';

import { HttpUtil, PromiseUtil } from '@/utils';
import { setMessageInstance } from '@/utils/messageBus';
import { useTheme } from '@/hooks/useTheme';
import { useAllSettings } from '@/api/queries/useAllSettings';
import { RESTART_FIELD_LABELS } from '@/lib/settings/restartKeys';
import { AllSettingSchema } from '@/schemas/setting';
import GeneralTab from './GeneralTab';
import BackupSection from './BackupSection';
import SectionRail from '@/components/ui/SectionRail';
import type { IconName } from '@/components/ui/Icon';
import { PageHeader } from '@/components/ui/panel';
import SecurityTab from './SecurityTab';
import TelegramTab from './TelegramTab';
import EmailTab from './EmailTab';
import SubscriptionGeneralTab from './SubscriptionGeneralTab';
import SubscriptionFormatsTab from './SubscriptionFormatsTab';
import './SettingsPage.css';

interface ApiMsg {
  success?: boolean;
}

const tabSlugs = ['general', 'security', 'telegram', 'email', 'subscription', 'subscription-formats', 'certs', 'tokens', 'backup', 'ext', 'time', 'ldap', 'iplimit', 'notifications'];

function isIp(h: string): boolean {
  if (typeof h !== 'string') return false;
  const v4 = h.split('.');
  if (v4.length === 4 && v4.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)) return true;
  if (!h.includes(':') || h.includes(':::')) return false;
  const parts = h.split('::');
  if (parts.length > 2) return false;
  const split = (s: string) => (s ? s.split(':').filter(Boolean) : []);
  const head = split(parts[0]);
  const tail = split(parts[1]);
  const valid = (seg: string) => /^[0-9a-fA-F]{1,4}$/.test(seg);
  if (![...head, ...tail].every(valid)) return false;
  const groups = head.length + tail.length;
  return parts.length === 2 ? groups < 8 : groups === 8;
}

export default function SettingsPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();

  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  const {
    allSetting,
    updateSetting,
    fetched,
    fetchError,
    retryFetch,
    spinning,
    setSpinning,
    saveDisabled,
    saveAll,
    savePayload,
    restartPendingKeys,
  } = useAllSettings();

  const [entryHost, setEntryHost] = useState('');
  const [entryPort, setEntryPort] = useState('');
  const [entryIsIP, setEntryIsIP] = useState(false);

  useEffect(() => {

    const host = window.location.hostname;
    setEntryHost(host);
    setEntryPort(window.location.port);
    setEntryIsIP(isIp(host));

  }, []);

  const [alertVisible, setAlertVisible] = useState(true);
  const [warningsExpanded, setWarningsExpanded] = useState(false);
  const [saveError, setSaveError] = useState('');
  const location = useLocation();
  const navigate = useNavigate();
  const slug = location.hash.replace(/^#/, '');
  const activeSlug = tabSlugs.includes(slug) ? slug : 'general';

  function rebuildUrlAfterRestart(): string {
    const { webDomain, webPort, webBasePath, webCertFile, webKeyFile } = allSetting;
    const newProtocol = (webCertFile || webKeyFile) ? 'https:' : 'http:';

    let base = webBasePath ? webBasePath.replace(/^\//, '') : '';
    if (base && !base.endsWith('/')) base += '/';

    if (!entryIsIP) {
      const url = new URL(window.location.href);
      url.pathname = `/${base}panel/settings`;
      url.protocol = newProtocol;
      url.hash = location.hash;
      url.search = location.search;
      return url.toString();
    }

    let finalHost = entryHost;
    let finalPort = entryPort || '';
    if (webDomain && isIp(webDomain)) finalHost = webDomain;
    if (webPort && Number(webPort) !== Number(entryPort)) finalPort = String(webPort);

    const url = new URL(`${newProtocol}//${finalHost}`);
    if (finalPort) url.port = finalPort;
    url.pathname = `/${base}panel/settings`;
    url.hash = location.hash;
    url.search = location.search;
    return url.toString();
  }

  async function onSave() {
    setSaveError('');
    const result = AllSettingSchema.safeParse(allSetting);
    if (!result.success) {
      const issue = result.error.issues[0];
      const fieldPath = issue?.path.join('.') ?? 'value';
      const msgKey = issue?.message ?? 'somethingWentWrong';
      messageApi.error(`${fieldPath}: ${t(msgKey, { defaultValue: msgKey })}`);
      return;
    }
    try {
      const result = await saveAll();
      if (!result.success) setSaveError(result.msg || t('somethingWentWrong'));
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : String(error));
    }
  }

  function restartPanel() {
    modal.confirm({
      title: t('pages.settings.restartPanel'),
      content: <>
        <p>{t('pages.settings.restartPanelDesc')}</p>
        <p>{t('pages.settings.restartPendingFor', { fields: restartPendingKeys.map((key) => t(RESTART_FIELD_LABELS[key] ?? key)).join(', ') })}</p>
      </>,
      okText: t('pages.settings.restartPanel'),
      okButtonProps: { danger: true },
      cancelText: t('cancel'),
      onOk: async () => {
        setSpinning(true);
        try {
          const msg = await HttpUtil.post('/panel/api/setting/restartPanel') as ApiMsg;
          if (!msg?.success) return;
          await PromiseUtil.sleep(5000);
          window.location.replace(rebuildUrlAfterRestart());
        } finally {
          setSpinning(false);
        }
      },
    });
  }

  const confAlerts = useMemo<string[]>(() => {
    const out: string[] = [];
    if (window.location.protocol !== 'https:') {
      out.push(t('pages.settings.warnHttp'));
    }
    if (allSetting.webPort === 2053) {
      out.push(t('pages.settings.warnDefaultPort'));
    }
    const segs = window.location.pathname.split('/').length < 4;
    if (segs && allSetting.webBasePath === '/') {
      out.push(t('pages.settings.warnDefaultBasePath'));
    }
    if (allSetting.subEnable) {
      let subPath = allSetting.subPath;
      if (allSetting.subURI) {
        try { subPath = new URL(allSetting.subURI).pathname; } catch { /* noop */ }
      }
      if (subPath === '/sub/') {
        out.push(t('pages.settings.warnDefaultSubPath'));
      }
    }
    if (allSetting.subJsonEnable) {
      let p = allSetting.subJsonPath;
      if (allSetting.subJsonURI) {
        try { p = new URL(allSetting.subJsonURI).pathname; } catch { /* noop */ }
      }
      if (p === '/json/') {
        out.push(t('pages.settings.warnDefaultJsonPath'));
      }
    }
    return out;
  }, [allSetting, t]);

  const pageClass = useMemo(() => {
    const classes = ['settings-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  const sections = useMemo<{ slug: string; labelKey: string; icon: IconName }[]>(() => {
    const list: { slug: string; labelKey: string; icon: IconName }[] = [
      { slug: 'general', labelKey: 'pages.settings.panelSettings', icon: 'settings' },
      { slug: 'notifications', labelKey: 'pages.settings.notifications', icon: 'bell' },
      { slug: 'certs', labelKey: 'pages.settings.certs', icon: 'shield' },
      { slug: 'tokens', labelKey: 'pages.nodes.apiToken', icon: 'api' },
      { slug: 'backup', labelKey: 'pages.index.backupTitle', icon: 'backup' },
      { slug: 'ext', labelKey: 'pages.settings.externalTraffic', icon: 'hosts' },
      { slug: 'time', labelKey: 'pages.settings.dateAndTime', icon: 'clock' },
      { slug: 'ldap', labelKey: 'design.ldap', icon: 'users' },
      { slug: 'iplimit', labelKey: 'pages.settings.ipLimit', icon: 'shield' },
      { slug: 'security', labelKey: 'pages.settings.securitySettings', icon: 'shield' },
      { slug: 'telegram', labelKey: 'pages.settings.TGBotSettings', icon: 'link' },
      { slug: 'email', labelKey: 'pages.settings.emailSettings', icon: 'mail' },
      { slug: 'subscription', labelKey: 'pages.settings.subSettings', icon: 'hosts' },
    ];
    if (allSetting.subJsonEnable || allSetting.subClashEnable || activeSlug === 'subscription-formats') {
      list.push({ slug: 'subscription-formats', labelKey: 'pages.settings.subFormats', icon: 'config' });
    }
    return list;
  }, [allSetting.subJsonEnable, allSetting.subClashEnable, activeSlug]);

  const activeSection = sections.find((x) => x.slug === activeSlug) ?? sections[0];

  const categoryBody = useMemo(() => {
    switch (activeSlug) {
      case 'notifications': return <GeneralTab section="2" allSetting={allSetting} updateSetting={updateSetting} />;
      case 'certs': return <GeneralTab section="3" allSetting={allSetting} updateSetting={updateSetting} />;
      case 'ext': return <GeneralTab section="4" allSetting={allSetting} updateSetting={updateSetting} />;
      case 'time': return <GeneralTab section="5" allSetting={allSetting} updateSetting={updateSetting} />;
      case 'ldap': return <GeneralTab section="6" allSetting={allSetting} updateSetting={updateSetting} />;
      case 'iplimit': return <GeneralTab section="7" allSetting={allSetting} updateSetting={updateSetting} />;
      case 'tokens': return <SecurityTab tokensOnly allSetting={allSetting} updateSetting={updateSetting} saveSetting={savePayload} />;
      case 'backup': return <BackupSection />;

      case 'security': return <SecurityTab allSetting={allSetting} updateSetting={updateSetting} saveSetting={savePayload} />;
      case 'telegram': return <TelegramTab allSetting={allSetting} updateSetting={updateSetting} />;
      case 'email': return <EmailTab allSetting={allSetting} updateSetting={updateSetting} />;
      case 'subscription': return <SubscriptionGeneralTab allSetting={allSetting} updateSetting={updateSetting} />;
      case 'subscription-formats': return <SubscriptionFormatsTab allSetting={allSetting} updateSetting={updateSetting} />;
      default: return <GeneralTab section="1" allSetting={allSetting} updateSetting={updateSetting} />;
    }
  }, [activeSlug, allSetting, updateSetting, savePayload]);

  const restartCount = restartPendingKeys.length;
  const restartHint = fetchError ? t('uiRepair.settings.refreshBeforeRestart') : !saveDisabled ? t('uiRepair.settings.saveBeforeRestart')
    : restartCount ? t('pages.settings.restartPendingFor', { fields: restartPendingKeys.map((key) => t(RESTART_FIELD_LABELS[key] ?? key)).join(', ') })
      : t('uiRepair.settings.noRestartPending');

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      {modalContextHolder}
      <div className={pageClass}>
        <PageHeader title={t('menu.settings')} sub={t('pages.settings.subtitle')} />
        {fetched && confAlerts.length > 0 && alertVisible && (
          <Alert type="warning" showIcon className="settings-warning"
            closable={{ closeIcon: true, onClose: () => setAlertVisible(false) }}
            title={<div className="settings-warning-summary">
              <span>{t('pages.settings.securityWarnings')} · {t('uiRepair.settings.warningCount', { count: confAlerts.length })}</span>
              <Button type="link" size="small" aria-expanded={warningsExpanded} aria-controls="settings-warning-details" onClick={() => setWarningsExpanded((value) => !value)}>
                {t(warningsExpanded ? 'uiRepair.settings.hideDetails' : 'uiRepair.settings.showDetails')}
              </Button>
            </div>}
            description={warningsExpanded && <div id="settings-warning-details">
              <p>{t('pages.settings.panelExposed')}</p>
              <ul>{confAlerts.map((warning) => <li key={warning}>{warning}</li>)}</ul>
            </div>}
          />
        )}
        {!fetched && fetchError ? (
          <Result status="error" title={t('uiRepair.settings.readFailed')} subTitle={fetchError.message}
            extra={<Button type="primary" onClick={() => { void retryFetch(); }}>{t('refresh')}</Button>} />
        ) : <Spin spinning={spinning || !fetched} delay={200} description={t('loading')} size="large">
          {!fetched ? <div className="settings-loading" /> : (
            <div className="p-settings settings-workspace">
              <SectionRail mobileCollapsible
                items={sections.map((item) => ({ key: item.slug, label: t(item.labelKey), desc: t(`pages.settings.sectionDesc_${item.slug.replace(/-/g, '_')}`), icon: item.icon }))}
                active={activeSlug}
                onPick={(next) => navigate({ pathname: location.pathname, search: location.search, hash: `#${next}` })}
              />
              <section className="settings-content" aria-labelledby="settings-section-heading">
                <div className="settings-section-header">
                  <div className="settings-section-title">
                    <h2 id="settings-section-heading">{t(activeSection.labelKey)}</h2>
                    <p>{t(`pages.settings.sectionDesc_${activeSlug.replace(/-/g, '_')}`)}</p>
                  </div>
                  <div className="settings-actions">
                    <span role="status" className={`settings-save-state${saveDisabled ? '' : ' is-dirty'}`}>
                      {saveDisabled ? t('pages.settings.allSaved') : t('pages.settings.unsaved')}
                    </span>
                    <Space size={8} wrap>
                      <Tooltip title={restartHint}><span>
                        <Button disabled={!saveDisabled || restartCount === 0 || spinning || !!fetchError} onClick={restartPanel}>
                          {restartCount ? t('uiRepair.settings.restartApply', { count: restartCount }) : t('pages.settings.restartPanel')}
                        </Button>
                      </span></Tooltip>
                      <Button type="primary" loading={spinning} disabled={saveDisabled} onClick={onSave}>{t('pages.settings.save')}</Button>
                    </Space>
                  </div>
                </div>
                {restartCount > 0 && <Alert type="warning" showIcon className="settings-pending" title={restartHint} />}
                {fetchError && <Alert type="warning" showIcon title={t('uiRepair.settings.readFailed')} description={fetchError.message} action={<Button onClick={() => { void retryFetch(); }}>{t('refresh')}</Button>} />}
                {saveError && <Alert type="error" showIcon title={t('uiRepair.settings.saveFailed')} description={saveError} closable={{ closeIcon: true, onClose: () => setSaveError('') }} />}
                {categoryBody}
              </section>
            </div>
          )}
        </Spin>}
        <FloatButton.BackTop visibilityHeight={200} />
      </div>
    </ConfigProvider>
  );
}
