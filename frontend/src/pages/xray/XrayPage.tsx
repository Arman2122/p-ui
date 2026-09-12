import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router';
import {
  Alert,
  Button,
  ConfigProvider,
  FloatButton,
  Layout,
  message,
  Radio,
  Result,
  Space,
  Spin,
} from 'antd';

import { useTheme } from '@/hooks/useTheme';
import SectionRail from '@/components/ui/SectionRail';
import { PageHeader } from '@/components/ui/panel';
import type { IconName } from '@/components/ui/Icon';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useXraySetting } from '@/hooks/useXraySetting';
import type { XraySettingsValue } from '@/hooks/useXraySetting';
import { JsonEditor } from '@/components/form';
import { setMessageInstance } from '@/utils/messageBus';

import { BasicsTab } from './basics';
import { propagateOutboundTagRename } from './basics/helpers';
import { RoutingTab } from './routing';
import { useRoutingDraft } from '@/hooks/useRoutingDraft';
import { OutboundsTab } from './outbounds';
import { BalancersTab } from './balancers';
import { cleanupOrphanedBalancerLoopbacks, ensureMissingBalancerLoopbacks, detectBalancerCycles } from './balancers/balancer-loopback';
import { DnsTab } from './dns';
import { WarpModal, NordModal } from './overrides';
import './XrayPage.css';
import { isExitKey } from '@/schemas/api/egress';
import CoreConfigsPage, { downloadConfig } from './CoreConfigsPage';
import { HttpUtil } from '@/utils';

const SECTION_SLUGS = ['basic', 'routing', 'outbound', 'balancer', 'dns', 'advanced'];

const XRAY_SECTIONS: { slug: string; labelKey: string; icon: IconName }[] = [
  { slug: 'basic', labelKey: 'pages.xray.basicTemplate', icon: 'config' },
  { slug: 'routing', labelKey: 'menu.routing', icon: 'routing' },
  { slug: 'outbound', labelKey: 'menu.outbounds', icon: 'outbounds' },
  { slug: 'balancer', labelKey: 'pages.xray.Balancers', icon: 'nodes' },
  { slug: 'dns', labelKey: 'pages.inbounds.info.dns', icon: 'hosts' },
  { slug: 'advanced', labelKey: 'pages.xray.advancedTemplate', icon: 'xray' },
];

type AdvKey = 'xraySetting' | 'inboundSettings' | 'outboundSettings' | 'routingRuleSettings';

export default function XrayPage({ section }: { section?: 'routing' | 'outbound' } = {}) {
  return section ? <XrayTemplatePage section={section} /> : <CoreConfigsPage xray={<XrayTemplatePage />} />;
}

function XrayTemplatePage({ section }: { section?: 'routing' | 'outbound' } = {}) {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => { setMessageInstance(messageApi); }, [messageApi]);
  const xs = useXraySetting();
  const {
    fetched,
    spinning,
    saveDisabled: templateSaveDisabled,
    fetchError,
    xraySetting,
    setXraySetting,
    templateSettings,
    setTemplateSettings,
    outboundTestUrl,
    setOutboundTestUrl,
    inboundTags,
    routingSubjects,
    clientReverseTags,
    subscriptionOutbounds,
    subscriptionOutboundTags,
    outboundsTraffic,
    outboundTestStates,
    subscriptionTestStates,
    testingAll,
    fetchAll,
    resetOutboundsTraffic,
    testOutbound,
    testExit,
    testSubscriptionOutbound,
    testAllOutbounds,
    saveAll,
    resetToDefault,
  } = xs;

  const [warpOpen, setWarpOpen] = useState(false);
  const [nordOpen, setNordOpen] = useState(false);
  const [advSettings, setAdvSettings] = useState<AdvKey>('xraySetting');
  const [invalidSlices, setInvalidSlices] = useState<Partial<Record<AdvKey, string>>>({});
  const [testingConfig, setTestingConfig] = useState(false);
  const [configValidation, setConfigValidation] = useState<{ text: string; error: boolean; document: string } | null>(null);
  const [saveFailure, setSaveFailure] = useState('');
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const location = useLocation();
  const navigate = useNavigate();
  const pathSection = section || (location.pathname === '/outbound' || location.pathname === '/outbounds' ? 'outbound' : location.pathname === '/routing' ? 'routing' : '');
  const sectionSlug = pathSection || location.hash.replace(/^#/, '');
  const activeSection = SECTION_SLUGS.includes(sectionSlug) ? sectionSlug : 'basic';

  const mutate = useCallback(
    (mutator: (next: XraySettingsValue) => void) => {
      setTemplateSettings((prev) => {
        if (!prev) return prev;
        const clone = JSON.parse(JSON.stringify(prev)) as XraySettingsValue;
        mutator(clone);
        return clone;
      });
    },
    [setTemplateSettings],
  );

  async function onTestOutbound(idx: number, mode: string) {
    if (isExitKey(idx)) {
      await testExit(idx, mode);
      return;
    }
    const outbound = templateSettings?.outbounds?.[idx];
    if (outbound) await testOutbound(idx, outbound, mode);
  }

  async function onTestSubscription(outbound: Record<string, unknown>, mode: string) {
    const tag = typeof outbound?.tag === 'string' ? outbound.tag : '';
    if (tag) await testSubscriptionOutbound(tag, outbound, mode);
  }

  function onAddOutbound(outbound: Record<string, unknown>) {
    mutate((tt) => {
      if (!Array.isArray(tt.outbounds)) tt.outbounds = [];
      tt.outbounds.push(outbound as never);
    });
  }
  function onResetOutbound(payload: { index: number; outbound: Record<string, unknown>; oldTag?: string; newTag?: string }) {
    mutate((tt) => {
      if (!tt.outbounds || payload.index < 0) return;
      tt.outbounds[payload.index] = payload.outbound as never;
      if (payload.oldTag && payload.newTag) {
        propagateOutboundTagRename(tt, payload.oldTag, payload.newTag);
      }
    });
  }
  function onRemoveOutboundByTag(tag: string) {
    mutate((tt) => {
      if (!tt.outbounds) return;
      const idx = tt.outbounds.findIndex((o) => o?.tag === tag);
      if (idx >= 0) tt.outbounds.splice(idx, 1);
    });
  }
  function onRemoveOutboundByIndex(index: number) {
    mutate((tt) => {
      if (tt.outbounds && index >= 0) tt.outbounds.splice(index, 1);
    });
  }
  function onRemoveRoutingRules(payload: { prefix: string }) {
    mutate((tt) => {
      const rules = tt.routing?.rules;
      if (!Array.isArray(rules)) return;
      tt.routing!.rules = rules.filter((r) => !r?.outboundTag?.startsWith?.(payload.prefix));
    });
  }

  const advancedText = useMemo(() => {
    if (invalidSlices[advSettings] !== undefined) return invalidSlices[advSettings]!;
    if (advSettings === 'xraySetting') return xraySetting;
    const tpl = templateSettings;
    if (!tpl) return '';
    try {
      switch (advSettings) {
        case 'inboundSettings': return JSON.stringify(tpl.inbounds || [], null, 2);
        case 'outboundSettings': return JSON.stringify(tpl.outbounds || [], null, 2);
        case 'routingRuleSettings': return JSON.stringify(tpl.routing?.rules || [], null, 2);
        default: return '';
      }
    } catch {
      return '';
    }
  }, [advSettings, xraySetting, templateSettings, invalidSlices]);

  function onAdvancedTextChange(next: string) {
    if (advSettings === 'xraySetting') {
      setXraySetting(next);
      return;
    }
    let parsed;
    try {
      parsed = JSON.parse(next);
      if (!Array.isArray(parsed)) throw new Error('expected array');
    } catch {
      setInvalidSlices((previous) => ({ ...previous, [advSettings]: next }));
      return;
    }
    setInvalidSlices((previous) => { const result = { ...previous }; delete result[advSettings]; return result; });
    mutate((tt) => {
      switch (advSettings) {
        case 'inboundSettings':
          tt.inbounds = parsed;
          break;
        case 'outboundSettings':
          tt.outbounds = parsed;
          break;
        case 'routingRuleSettings':
          if (!tt.routing) tt.routing = {};
          tt.routing.rules = parsed;
          break;
      }
    });
  }

  async function onSaveAll() {
    if (savingRef.current || routing.loading || routing.error) return;
    if (Object.keys(invalidSlices).length) {
      messageApi.error(t('settingsOps.core.invalidJSON'));
      navigate('/xray#advanced');
      return;
    }
    try {
      JSON.parse(xraySetting);
    } catch (e) {
      messageApi.error(`Advanced JSON: ${(e as Error).message}`);
      navigate('/xray#advanced');
      return;
    }
    let snapshot = xraySetting;
    if (templateSettings) {
      const clone = JSON.parse(JSON.stringify(templateSettings));
      ensureMissingBalancerLoopbacks(clone);
      cleanupOrphanedBalancerLoopbacks(clone);
      const cycles = detectBalancerCycles(clone);
      if (cycles.length > 0) {
        const names = cycles.map((c) => c.join(' → ')).join(', ');
        messageApi.error(t('pages.xray.balancer.balancerFallbackCycle') + ' (' + names + ')');
        return;
      }
      const serialized = JSON.stringify(clone, null, 2);
      snapshot = serialized;
      setXraySetting(serialized);
      setTemplateSettings(clone);
    }
    if (savingRef.current) return;
    savingRef.current = true;
    setSaving(true);
    setSaveFailure('');
    try {
      await routing.flush();
      await saveAll(snapshot);
    } catch (error) {
      setSaveFailure(error instanceof Error ? error.message : String(error));
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  async function testConfig() {
    if (Object.keys(invalidSlices).length) {
      messageApi.error(t('settingsOps.core.invalidJSON'));
      return;
    }
    setTestingConfig(true);
    try {
      const result = await HttpUtil.post('/panel/api/core-config/xray/validate', { document: xraySetting }, { timeout: 40_000, silentSuccess: true });
      setConfigValidation({ text: result.success ? t('settingsOps.core.templateValidated') : result.msg, error: !result.success, document: xraySetting });
    } catch (error) {
      setConfigValidation({ text: error instanceof Error ? error.message : String(error), error: true, document: xraySetting });
    } finally {
      setTestingConfig(false);
    }
  }

  /* Routing rules are drafted like the template, so Save has to see them too;
     without this the button stays dark while unsaved rules sit on screen. */
  const routing = useRoutingDraft();
  const saveDisabled = templateSaveDisabled && !routing.dirty && !Object.keys(invalidSlices).length;

  const scrollTarget = () => document.getElementById('content-layout') || window;

  const pageClass = `xray-page ${isDark ? 'is-dark' : ''} ${isUltra ? 'is-ultra' : ''}`.trim();

  const saveActions = <Space wrap size={8}>
    <span className="p-sub" role="status">{routing.loading ? t('loading') : routing.error ? t('somethingWentWrong') : saveDisabled ? t('pages.settings.allSaved') : t('pages.settings.unsaved')}</span>
    <Button type="primary" disabled={saveDisabled || routing.loading || !!routing.error} loading={saving} onClick={() => { void onSaveAll(); }}>{t('pages.xray.save')}</Button>
  </Space>;
  const renderNetworkHeader = (actions: ReactNode) => <>
    <PageHeader title={t(pathSection === 'routing' ? 'menu.routing' : 'menu.outbounds')}
      sub={t(pathSection === 'routing' ? 'routingPage.subtitle' : 'pages.xray.sectionDesc_outbound')}
      actions={<>{actions}{saveActions}</>} />
    {saveFailure && <Alert type="error" showIcon title={t('somethingWentWrong')} description={t(saveFailure)} />}
  </>;

  const sectionBody = (() => {
    switch (activeSection) {
      case 'routing':
        return (
          <RoutingTab
            standalone={pathSection === 'routing'}
            renderHeader={pathSection === 'routing' ? renderNetworkHeader : undefined}
            hasUnsavedChanges={!saveDisabled}
            templateSettings={templateSettings}
            setTemplateSettings={setTemplateSettings}
            inboundTags={inboundTags}
            routingSubjects={routingSubjects}
            clientReverseTags={clientReverseTags}
            subscriptionOutboundTags={subscriptionOutboundTags}
            isMobile={isMobile}
            routing={routing}
          />
        );
      case 'outbound':
        return (
          <OutboundsTab
            standalone={pathSection === 'outbound'}
            renderHeader={pathSection === 'outbound' ? renderNetworkHeader : undefined}
            templateSettings={templateSettings}
            setTemplateSettings={setTemplateSettings}
            outboundsTraffic={outboundsTraffic}
            outboundTestStates={outboundTestStates}
            subscriptionTestStates={subscriptionTestStates}
            testingAll={testingAll}
            inboundTags={inboundTags}
            subscriptionOutbounds={subscriptionOutbounds}
            subscriptionOutboundTags={subscriptionOutboundTags}
            isMobile={isMobile}
            onResetTraffic={resetOutboundsTraffic}
            onTest={onTestOutbound}
            onTestSubscription={onTestSubscription}
            onTestAll={testAllOutbounds}
            onShowWarp={() => setWarpOpen(true)}
            onShowNord={() => setNordOpen(true)}
            onRefreshXrayData={fetchAll}
          />
        );
      case 'balancer':
        return (
          <BalancersTab
            templateSettings={templateSettings}
            setTemplateSettings={setTemplateSettings}
            clientReverseTags={clientReverseTags}
            subscriptionOutboundTags={subscriptionOutboundTags}
            isMobile={isMobile}
          />
        );
      case 'dns':
        return (
          <DnsTab
            templateSettings={templateSettings}
            setTemplateSettings={setTemplateSettings}
          />
        );
      case 'advanced':
        return (
          <>
            <div className="advanced-meta">
              <h4>{t('pages.xray.Template')}</h4>
              <p>{t('pages.xray.TemplateDesc')}</p>
            </div>
            <Radio.Group
              value={advSettings}
              buttonStyle="solid"
              size={isMobile ? 'small' : 'middle'}
              style={{ margin: '12px 0' }}
              onChange={(e) => setAdvSettings(e.target.value)}
            >
              <Radio.Button value="xraySetting">{t('pages.xray.completeTemplate')}</Radio.Button>
              <Radio.Button value="inboundSettings">{t('pages.xray.Inbounds')}</Radio.Button>
              <Radio.Button value="outboundSettings">{t('pages.xray.Outbounds')}</Radio.Button>
              <Radio.Button value="routingRuleSettings">{t('pages.xray.Routings')}</Radio.Button>
            </Radio.Group>
            <JsonEditor
              value={advancedText}
              onChange={onAdvancedTextChange}
              minHeight="420px"
              maxHeight="720px"
            />
          </>
        );
      default:
        return (
          <BasicsTab
            templateSettings={templateSettings}
            setTemplateSettings={setTemplateSettings}
            outboundTestUrl={outboundTestUrl}
            onChangeOutboundTestUrl={setOutboundTestUrl}
            onResetDefault={resetToDefault}
          />
        );
    }
  })();

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>

        <Layout className="content-shell">
          <div id="content-layout" className="ant-layout-content content-area">
            <Spin spinning={spinning || !fetched} delay={200} description={t('loading')} size="large">
              {!fetched ? (
                <div className="loading-spacer" />
              ) : fetchError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={fetchError}
                  extra={<Button type="primary" onClick={fetchAll}>{t('check')}</Button>}
                />
              ) : (
                <div className="p-network-page">
                  <FloatButton.BackTop target={scrollTarget} visibilityHeight={200} />
                  {pathSection ? sectionBody : <>
                  <div className="p-settings">
                    <SectionRail
                      items={XRAY_SECTIONS.map((x) => ({
                        key: x.slug,
                        label: t(x.labelKey),
                        desc: t(`pages.xray.sectionDesc_${x.slug}`),
                        icon: x.icon,
                      }))}
                      active={activeSection}
                      onPick={(slug) => navigate(slug === 'routing' || slug === 'outbound' ? `/${slug}` : `/xray#${slug}`)}
                    />
                    <section className="p-card p-core-editor">
                      <div className="p-card-h">
                        <div>
                          <div style={{ fontSize: 15, fontWeight: 650, color: 'var(--p-text)' }}>
                            {t(XRAY_SECTIONS.find((x) => x.slug === activeSection)?.labelKey ?? 'menu.xray')}
                          </div>
                          <div className="p-sub" style={{ marginTop: 2 }}>{t(`pages.xray.sectionDesc_${activeSection}`)}</div>
                        </div>
                        <Space wrap size={8}>
                          {saveActions}
                          <Button loading={testingConfig} disabled={saving || routing.dirty} onClick={testConfig}>{t('settingsOps.core.validate')}</Button>
                          <Button onClick={() => downloadConfig('xray-template.json', xraySetting)}>{t('download')}</Button>
                        </Space>
                      </div>
                      {saveFailure && <Alert type="error" showIcon title={t('somethingWentWrong')} description={t(saveFailure)} />}
                      {configValidation?.document === xraySetting && <Alert type={configValidation.error ? 'error' : 'success'} title={configValidation.text} showIcon />}
                      <div className="p-core-editor-body">{sectionBody}</div>
                    </section>
                  </div>
                  </>}
                </div>
              )}
            </Spin>
          </div>
        </Layout>

        <WarpModal
          open={warpOpen}
          templateSettings={templateSettings}
          onClose={() => setWarpOpen(false)}
          onAddOutbound={onAddOutbound}
          onResetOutbound={onResetOutbound}
          onRemoveOutbound={onRemoveOutboundByTag}
        />
        <NordModal
          open={nordOpen}
          templateSettings={templateSettings}
          onClose={() => setNordOpen(false)}
          onAddOutbound={onAddOutbound}
          onResetOutbound={onResetOutbound}
          onRemoveOutbound={onRemoveOutboundByIndex}
          onRemoveRoutingRules={onRemoveRoutingRules}
        />
      </Layout>
    </ConfigProvider>
  );
}
