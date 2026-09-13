import { lazy, useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { Button, ConfigProvider, Layout, Modal, Result, Space, Spin, message } from 'antd';
import { CopyOutlined, CloudDownloadOutlined } from '@ant-design/icons';

import { HttpUtil, ClipboardManager, FileManager } from '@/utils';
import { USAGE_CRIT_COLOR, USAGE_CRIT_PERCENT, USAGE_WARN_COLOR, USAGE_WARN_PERCENT } from '@/models/status';
import { useTheme } from '@/hooks/useTheme';
import { useStatusQuery } from '@/api/queries/useStatusQuery';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { LazyMount } from '@/components/utility';
import { PanelCard } from '@/components/ui/panel';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import { useAllSettings } from '@/api/queries/useAllSettings';
import { useRelativeTime } from '@/hooks/useRelativeTime';
import { summarizeCores } from '@/lib/cores/status';
import { setMessageInstance } from '@/utils/messageBus';
import OverviewActionBar from './OverviewActionBar';
import VitalsBand, { type VitalKey } from './VitalsBand';
import DashboardCores, { CoreSummaryStrip } from './DashboardCores';
import { ActivityPanel, PanelHostPanel } from './DashboardPanels';
import { HistoryWindowControl, SessionsPanel, ThroughputPanel } from './DashboardTelemetry';
import { useOverviewHistory } from './useOverviewHistory';
import type { PanelUpdateInfo } from './PanelUpdateModal';
const JsonEditor = lazy(() => import('@/components/form/JsonEditor'));
const PanelUpdateModal = lazy(() => import('./PanelUpdateModal'));
const LogModal = lazy(() => import('./LogModal'));
const BackupModal = lazy(() => import('./BackupModal'));
const SystemHistoryModal = lazy(() => import('./SystemHistoryModal'));
const XrayMetricsModal = lazy(() => import('./XrayMetricsModal'));
const XrayLogModal = lazy(() => import('./XrayLogModal'));
const VersionModal = lazy(() => import('./VersionModal'));
import './IndexPage.css';

export default function IndexPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { status, fetched, fetchError, refresh } = useStatusQuery();
  const { isMobile } = useMediaQuery();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => { setMessageInstance(messageApi); }, [messageApi]);

  const [accessLogEnable, setAccessLogEnable] = useState(false);
  const [devChannelEnable, setDevChannelEnable] = useState(false);
  const [panelUpdateInfo, setPanelUpdateInfo] = useState<PanelUpdateInfo>({
    currentVersion: '',
    latestVersion: '',
    updateAvailable: false,
  });

  const [showIp, setShowIp] = useState(false);
  const [logsOpen, setLogsOpen] = useState(false);
  const [backupOpen, setBackupOpen] = useState(false);
  const [panelUpdateOpen, setPanelUpdateOpen] = useState(false);
  const [sysHistoryOpen, setSysHistoryOpen] = useState(false);
  const [xrayMetricsOpen, setXrayMetricsOpen] = useState(false);
  const [xrayLogsOpen, setXrayLogsOpen] = useState(false);
  const [versionOpen, setVersionOpen] = useState(false);
  const [configTextOpen, setConfigTextOpen] = useState(false);
  const [configText, setConfigText] = useState('');
  const [loading, setLoading] = useState(false);
  const [loadingTip, setLoadingTip] = useState(t('loading'));

  const [historyBucket, setHistoryBucket] = useState(60);
  const history = useOverviewHistory(status, fetched && !fetchError, historyBucket);
  const { cores } = useCoresStatusQuery();
  const coreSummary = summarizeCores(cores);
  const navigate = useNavigate();
  const goToCores = useCallback(() => navigate('/cores'), [navigate]);
  const [openVital, setOpenVital] = useState<VitalKey | null>(null);
  const { totals: nodeTotals } = useNodesQuery();
  const { allSetting } = useAllSettings();
  const relative = useRelativeTime();

  useEffect(() => {
    HttpUtil.post<{ accessLogEnable?: boolean; devChannelEnable?: boolean }>(
      '/panel/api/setting/defaultSettings',
    ).then((msg) => {
      if (msg?.success && msg.obj) {
        setAccessLogEnable(!!msg.obj.accessLogEnable);
        setDevChannelEnable(!!msg.obj.devChannelEnable);
      }
    });
    HttpUtil.get<PanelUpdateInfo>('/panel/api/server/getPanelUpdateInfo').then((msg) => {
      if (msg?.success && msg.obj) setPanelUpdateInfo(msg.obj);
    });
  }, []);

  const displayVersion = useMemo(
    () => window.PUI_CUR_VER || panelUpdateInfo.currentVersion || '?',
    [panelUpdateInfo.currentVersion],
  );

  const setBusy = useCallback(
    ({ busy, tip }: { busy: boolean; tip?: string }) => {
      setLoading(busy);
      if (tip) setLoadingTip(tip);
    },
    [],
  );

  const stopXray = useCallback(async () => {
    await HttpUtil.post('/panel/api/server/stopXrayService');
    await refresh();
  }, [refresh]);

  const restartXray = useCallback(async () => {
    await HttpUtil.post('/panel/api/server/restartXrayService');
    await refresh();
  }, [refresh]);

  async function handleChannelChange(dev: boolean) {
    const res = await HttpUtil.post('/panel/api/server/setUpdateChannel', { dev });
    if (!res?.success) return;
    setDevChannelEnable(dev);
    const msg = await HttpUtil.get<PanelUpdateInfo>('/panel/api/server/getPanelUpdateInfo');
    if (msg?.success && msg.obj) setPanelUpdateInfo(msg.obj);
  }

  async function openConfig() {
    setLoading(true);
    try {
      const msg = await HttpUtil.get('/panel/api/server/getConfigJson');
      if (!msg?.success) return;
      setConfigText(JSON.stringify(msg.obj, null, 2));
      setConfigTextOpen(true);
    } finally {
      setLoading(false);
    }
  }

  async function copyConfig() {
    const ok = await ClipboardManager.copyText(configText || '');
    if (ok) messageApi.success('Copied');
  }

  function downloadConfig() {
    FileManager.downloadTextFile(configText, 'config.json');
  }

  const pageClass = `index-page ${isDark ? 'is-dark' : ''} ${isUltra ? 'is-ultra' : ''}`.trim();

  const health = useMemo(() => {
    const items = [
      { name: t('pages.index.cpu'), value: status.cpu.percent },
      { name: t('pages.index.memory'), value: status.mem.percent },
      { name: t('pages.index.swap'), value: status.swap.percent },
      { name: t('pages.index.storage'), value: status.disk.percent },
    ];
    const list = (xs: typeof items) => xs.map((i) => `${i.name} ${i.value.toFixed(0)}%`).join(', ');
    const crit = items.filter((i) => i.value >= USAGE_CRIT_PERCENT);
    if (crit.length) return { text: t('pages.index.healthCritical', { list: list(crit) }), color: USAGE_CRIT_COLOR };
    const warm = items.filter((i) => i.value >= USAGE_WARN_PERCENT);
    if (warm.length) return { text: t('pages.index.healthWarm', { list: list(warm) }), color: USAGE_WARN_COLOR };
    return null;
  }, [status, t]);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>

        <Layout className="content-shell">
          <div className="ant-layout-content content-area">
            <Spin
              spinning={loading || !fetched}
              delay={200}
              description={loading ? loadingTip : t('loading')}
              size="large"
            >
              {!fetched ? (
                <div className="loading-spacer" />
              ) : fetchError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={fetchError}
                  extra={<Button type="primary" onClick={refresh}>{t('refresh')}</Button>}
                />
              ) : (
                <div className="ov-page">
                  <OverviewActionBar
                    historyControl={<HistoryWindowControl value={historyBucket} onChange={setHistoryBucket} />}
                    onRefresh={refresh}
                    status={status}
                    isMobile={isMobile}
                    accessLogEnable={accessLogEnable}
                    panelVersion={displayVersion}
                    latestVersion={panelUpdateInfo.latestVersion}
                    updateAvailable={panelUpdateInfo.updateAvailable}
                    onStopXray={stopXray}
                    onRestartXray={restartXray}
                    onOpenLogs={() => setLogsOpen(true)}
                    onOpenXrayLogs={() => setXrayLogsOpen(true)}
                    onOpenConfig={openConfig}
                    onOpenBackup={() => setBackupOpen(true)}
                    onOpenSystemHistory={() => setSysHistoryOpen(true)}
                    onOpenXrayMetrics={() => setXrayMetricsOpen(true)}
                    onOpenPanelUpdate={() => setPanelUpdateOpen(true)}
                    onOpenVersionSwitch={() => setVersionOpen(true)}
                  />

                  {health && (
                    <div className="ov-health" style={{ color: health.color }}>
                      <span className="ov-health-mark" />
                      {health.text}
                    </div>
                  )}


                  <VitalsBand
                    status={status}
                    history={history}
                    open={openVital}
                    onOpen={setOpenVital}
                    lead={<CoreSummaryStrip cores={cores} onOpen={goToCores} />}
                  />

                  <PanelCard
                    kicker={t('menu.cores')}
                    icon="xray"
                    pad={12}
                    className="ov-cores-panel"
                    extra={<Space size={8}><span className="p-sub">{t('pages.index.coresRunning', { running: coreSummary.running, total: coreSummary.total })}</span>
                      <Button type="text" size="small" onClick={goToCores}>{t('menu.manageCores')}</Button></Space>}
                  >
                    <DashboardCores cores={cores} onOpen={goToCores} />
                  </PanelCard>

                  <div className="ov-telemetry-grid">
                    <ThroughputPanel status={status} history={history} />
                    <SessionsPanel status={status} history={history} />
                  </div>

                  <div className="ov-bottom-grid">
                    <ActivityPanel relative={relative} />
                    <PanelHostPanel
                      status={status}
                      nodeCount={nodeTotals.total}
                      webPort={allSetting.webPort}
                      basePath={window.PUI_BASE_PATH || allSetting.webBasePath}
                      sessionMinutes={allSetting.sessionMaxAge}
                      showIp={showIp}
                      onToggleIp={() => setShowIp((v) => !v)}
                    />
                  </div>
                </div>
              )}
            </Spin>
          </div>
        </Layout>

        <LazyMount when={panelUpdateOpen}>
          <PanelUpdateModal
            open={panelUpdateOpen}
            info={panelUpdateInfo}
            devChannelEnable={devChannelEnable}
            onChannelChange={handleChannelChange}
            onClose={() => setPanelUpdateOpen(false)}
            onBusy={setBusy}
          />
        </LazyMount>
        <LazyMount when={logsOpen}>
          <LogModal open={logsOpen} onClose={() => setLogsOpen(false)} />
        </LazyMount>
        <LazyMount when={backupOpen}>
          <BackupModal
            open={backupOpen}
            onClose={() => setBackupOpen(false)}
            onBusy={setBusy}
          />
        </LazyMount>
        <LazyMount when={sysHistoryOpen}>
          <SystemHistoryModal
            open={sysHistoryOpen}
            status={status}
            onClose={() => setSysHistoryOpen(false)}
          />
        </LazyMount>
        <LazyMount when={xrayMetricsOpen}>
          <XrayMetricsModal open={xrayMetricsOpen} onClose={() => setXrayMetricsOpen(false)} />
        </LazyMount>
        <LazyMount when={xrayLogsOpen}>
          <XrayLogModal open={xrayLogsOpen} onClose={() => setXrayLogsOpen(false)} />
        </LazyMount>
        <LazyMount when={versionOpen}>
          <VersionModal
            open={versionOpen}
            status={status}
            onClose={() => setVersionOpen(false)}
            onBusy={setBusy}
          />
        </LazyMount>

        <LazyMount when={configTextOpen}>
          <Modal
            open={configTextOpen}
            title={t('pages.index.config')}
            width={isMobile ? '100%' : 900}
            style={isMobile
              ? { top: 20, maxWidth: 'calc(100vw - 16px)' }
              : { top: 20 }}
            onCancel={() => setConfigTextOpen(false)}
            footer={[
              <Button
                key="download"
                onClick={downloadConfig}
                size={isMobile ? 'small' : 'middle'}
                icon={<CloudDownloadOutlined />}
              >
                {isMobile ? 'Download' : 'config.json'}
              </Button>,
              <Button
                key="copy"
                type="primary"
                onClick={copyConfig}
                size={isMobile ? 'small' : 'middle'}
                icon={<CopyOutlined />}
              >
                Copy
              </Button>,
            ]}
          >
            <JsonEditor
              value={configText}
              onChange={setConfigText}
              minHeight={isMobile ? '300px' : 'calc(100vh - 220px)'}
              maxHeight={isMobile ? '70vh' : 'calc(100vh - 220px)'}
              readOnly
            />
          </Modal>
        </LazyMount>
      </Layout>
    </ConfigProvider>
  );
}
