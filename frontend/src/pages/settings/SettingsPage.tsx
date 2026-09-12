import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router';
import {
  Alert,
  Button,
  ConfigProvider,
  FloatButton,
  Layout,
  Modal,
  Space,
  Spin,
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

function scrollTarget() {
  return document.getElementById('content-layout') as HTMLElement;
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
      return url.toString();
    }

    let finalHost = entryHost;
    let finalPort = entryPort || '';
    if (webDomain && isIp(webDomain)) finalHost = webDomain;
    if (webPort && Number(webPort) !== Number(entryPort)) finalPort = String(webPort);

    const url = new URL(`${newProtocol}//${finalHost}`);
    if (finalPort) url.port = finalPort;
    url.pathname = `/${base}panel/settings`;
    return url.toString();
  }

  async function onSave() {
    const result = AllSettingSchema.safeParse(allSetting);
    if (!result.success) {
      const issue = result.error.issues[0];
      const fieldPath = issue?.path.join('.') ?? 'value';
      const msgKey = issue?.message ?? 'somethingWentWrong';
      messageApi.error(`${fieldPath}: ${t(msgKey, { defaultValue: msgKey })}`);
      return;
    }
    await saveAll();
  }

  function restartPanel() {
    modal.confirm({
      title: t('pages.settings.restartPanel'),
      content: t('pages.settings.restartPanelDesc'),
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
    if (allSetting.subJsonEnable || allSetting.subClashEnable) {
      list.push({ slug: 'subscription-formats', labelKey: 'pages.settings.subFormats', icon: 'config' });
    }
    return list;
  }, [allSetting.subJsonEnable, allSetting.subClashEnable]);

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

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      {modalContextHolder}
      <Layout className={pageClass}>

        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin spinning={spinning || !fetched} delay={200} description={t('loading')} size="large">
              {!fetched ? (
                <div className="loading-spacer" />
              ) : (
                <>
                  {confAlerts.length > 0 && alertVisible && (
                    <Alert
                      type="error"
                      showIcon
                      closable={{ onClose: () => setAlertVisible(false) }}
                      className="conf-alert"
                      title={t('pages.settings.securityWarnings')}
                      description={(
                        <>
                          <b>{t('pages.settings.panelExposed')}</b>
                          <ul>
                            {confAlerts.map((msg, i) => <li key={i}>{msg}</li>)}
                          </ul>
                        </>
                      )}
                    />
                  )}

                  <PageHeader title={t('menu.settings')} sub={t('pages.settings.subtitle')} />

                  <FloatButton.BackTop target={scrollTarget} visibilityHeight={200} />

                  {restartPendingKeys.length > 0 && (
                    <Alert
                      type="warning"
                      showIcon
                      title={t('pages.settings.restartPendingFor', {
                        fields: restartPendingKeys.map((key) => t(RESTART_FIELD_LABELS[key] ?? key)).join(', '),
                      })}
                    />
                  )}

                  <div className="p-settings">
                    <SectionRail
                      items={sections.map((x) => ({
                        key: x.slug,
                        label: t(x.labelKey),
                        desc: t(`pages.settings.sectionDesc_${x.slug.replace(/-/g, '_')}`),
                        icon: x.icon,
                      }))}
                      active={activeSlug}
                      onPick={(slug) => navigate(`/settings#${slug}`)}
                    />
                    <div style={{ minWidth: 0, display: 'flex', flexDirection: 'column', gap: 12 }}>
                      <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap' }}>
                        <div>
                          <div style={{ fontSize: 15, fontWeight: 650, color: 'var(--p-text)' }}>
                            {t(activeSection.labelKey)}
                          </div>
                          <div className="p-sub" style={{ marginTop: 2 }}>
                            {t(`pages.settings.sectionDesc_${activeSlug.replace(/-/g, '_')}`)}
                          </div>
                        </div>
                        <Space size={8}>
                          <span className="p-sub">
                            {saveDisabled ? t('pages.settings.allSaved') : t('pages.settings.unsaved')}
                          </span>
                          <Button type="primary" danger disabled={!saveDisabled} onClick={restartPanel}>
                            {t('pages.settings.restartPanel')}
                          </Button>
                          <Button type="primary" disabled={saveDisabled} onClick={onSave}>
                            {t('pages.settings.save')}
                          </Button>
                        </Space>
                      </div>
                      {categoryBody}
                    </div>
                  </div>
                </>
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
