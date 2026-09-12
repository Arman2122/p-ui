import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, ConfigProvider, Modal, Result, Segmented, Spin, message } from 'antd';
import { useTheme } from '@/hooks/useTheme';
import { orderCores } from '@/lib/cores/display';
import { useLiveTrafficHistory } from '@/hooks/useLiveTrafficHistory';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import { setMessageInstance } from '@/utils/messageBus';
import { FileManager, HttpUtil } from '@/utils';
import { StatStrip } from '@/components/ui';
import { PageHeader, Toolbar } from '@/components/ui/panel';
import { CoreVersionsViewSchema, type CoreStatusView, type CoreVersionsView } from '@/generated/zod';
import { Icon } from '@/components/ui/Icon';
import CoreDetailModal from './CoreDetailModal';
import { CoreCard, CoreRow, coreBytes, type CoreTab } from './CorePresentation';
import { coreInstalled, coreMetrics, newerCoreVersion, useCoreInventory } from './useCoreInventory';
import './CoresPage.css';

export default function CoresPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => { setMessageInstance(messageApi); }, [messageApi]);
  const { cores: reportedCores, loading, fetched, fetchError, refetch } = useCoresStatusQuery();
  const cores = orderCores(reportedCores);
  const inventory = useCoreInventory();
  const [view, setView] = useState('rows');
  const [detail, setDetail] = useState<{ id: string; tab: CoreTab } | null>(null);
  const [restarting, setRestarting] = useState<string[]>([]);
  const [checking, setChecking] = useState(false);
  const [versions, setVersions] = useState<Record<string, CoreVersionsView>>({});
  const [actionErrors, setActionErrors] = useState<string[]>([]);
  const selected = cores.find((core) => core.id === detail?.id);
  const rows = cores.map((core) => {
    const descriptor = inventory.descriptors?.find((item) => item.id === core.id);
    return { core, descriptor, metrics: coreMetrics(descriptor, inventory.inbounds), update: newerCoreVersion(core, versions[core.id]) };
  });
  const trafficHistory = useLiveTrafficHistory({
    sampleAt: inventory.observedAt,
    samples: rows.filter((row) => row.core.available && !row.core.trafficError && row.metrics.inbounds?.length && row.metrics.traffic !== undefined).map((row) => ({
      key: row.core.id, scopeKey: row.descriptor?.kinds.join(','),
      counters: Object.fromEntries(row.metrics.inbounds!.flatMap((inbound) => [[`${inbound.id}:up`, inbound.up], [`${inbound.id}:down`, inbound.down]])),
    })),
  });
  const updates = rows.every((row) => row.update !== undefined) ? rows.filter((row) => row.update).length : undefined;
  const traffic = rows.length && rows.every((row) => row.metrics.traffic !== undefined) ? rows.reduce((sum, row) => sum + row.metrics.traffic!, 0) : undefined;
  const running = cores.filter((core) => core.available && core.ready && core.state === 'running').length;
  const stopped = cores.filter((core) => core.available && (core.state === 'stopped' || core.state === 'idle')).length;

  const restart = useCallback((targets: CoreStatusView[], all = false) => {
    const eligible = targets.filter((core) => core.available);
    if (!eligible.length) return;
    modal.confirm({
      title: all ? t('pages.cores.fidelity.restartAllConfirm') : t('pages.cores.restartConfirm', { core: t(eligible[0].titleKey) }),
      content: <><p>{t('pages.cores.restartNote')}</p>{all && <p>{eligible.map((core) => t(core.titleKey)).join(', ')}</p>}</>,
      okText: all ? t('pages.cores.fidelity.restartAll') : t('pages.cores.restart'), cancelText: t('cancel'),
      onOk: async () => {
        const failed: string[] = [];
        setActionErrors([]);
        setRestarting(eligible.map((core) => core.id));
        try {
          for (const core of eligible) {
            try {
              const msg = await HttpUtil.post(`/panel/api/cores/${core.id}/restart`, undefined, { silentSuccess: true });
              if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
            } catch (error) { failed.push(`${t(core.titleKey)}: ${error instanceof Error ? error.message : t('somethingWentWrong')}`); }
          }
          setActionErrors(failed);
          await refetch();
        } finally { setRestarting([]); }
      },
    });
  }, [modal, t, refetch]);

  async function checkVersions() {
    setChecking(true);
    setActionErrors([]);
    const results: Record<string, CoreVersionsView> = {};
    const failed: string[] = [];
    try {
      await Promise.all(cores.filter((core) => core.version.manageable).map(async (core) => {
        try {
          const msg = await HttpUtil.get(`/panel/api/cores/${core.id}/versions`, undefined, { silentSuccess: true });
          if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
          const result = CoreVersionsViewSchema.parse(msg.obj);
          results[core.id] = result;
          if (result.error) failed.push(`${t(core.titleKey)}: ${result.error}`);
        } catch (error) { failed.push(`${t(core.titleKey)}: ${error instanceof Error ? error.message : t('somethingWentWrong')}`); }
      }));
      setVersions(results);
      setActionErrors(failed);
      await refetch();
    } finally { setChecking(false); }
  }

  function exportStatus() {
    const snapshot = { capturedAt: new Date().toISOString(), cores: rows.map(({ core, metrics, update }) => ({
      ...core, inboundCount: metrics.inbounds?.length ?? null, clientCount: metrics.clients ?? null,
      trafficBytes: metrics.traffic ?? null, liveClients: null,
      trafficHistory: trafficHistory[core.id]?.points.length ? trafficHistory[core.id].points : null, updateVersion: update ?? null,
      versionChecked: !!versions[core.id],
    })) };
    FileManager.downloadTextFile(JSON.stringify(snapshot, null, 2), 'cores-status.json', { type: 'application/json' });
  }

  return <ConfigProvider theme={antdThemeConfig}>
    {messageContextHolder}{modalContextHolder}
    <section className={`cores-page${isDark ? ' is-dark' : ''}${isUltra ? ' is-ultra' : ''}`} aria-label={t('pages.cores.title')}>
      <Spin spinning={!fetched} delay={200}>
        {!fetched ? <div className="loading-spacer" /> : fetchError ? <Result status="error" title={t('somethingWentWrong')} subTitle={fetchError}
          extra={<Button loading={loading} onClick={() => void refetch()}>{t('refresh')}</Button>} /> : <div className="cores-content">
          <PageHeader title={t('menu.cores')} sub={t('pages.cores.fidelity.subtitle')} actions={<>
            <Segmented size="small" value={view} onChange={setView} options={[{ value: 'cards', label: t('pages.cores.fidelity.cards') }, { value: 'rows', label: t('pages.cores.fidelity.rows') }]} />
            <Button icon={<Icon name="refresh" size={15} />} loading={checking} onClick={() => void checkVersions()}>{t('pages.cores.fidelity.checkVersions')}</Button>
            <Button icon={<Icon name="restart" size={15} />} loading={restarting.length > 0} disabled={!cores.some((core) => core.available)} onClick={() => restart(cores, true)}>{t('pages.cores.fidelity.restartAll')}</Button>
          </>} />
          <StatStrip items={[
            { key: 'installed', label: t('pages.cores.fidelity.installed'), value: cores.filter(coreInstalled).length },
            { key: 'running', label: t('pages.cores.state.running'), value: running, color: 'var(--p-success)', pulse: running > 0 },
            { key: 'stopped', label: t('pages.cores.state.stopped'), value: stopped, color: '#8b8d94', detail: t('pages.cores.fidelity.stoppedHelp') },
            { key: 'updates', label: t('pages.cores.fidelity.updates'), value: updates ?? '—', color: updates ? 'var(--p-warning)' : undefined, detail: t('pages.cores.fidelity.updatesHelp') },
            { key: 'traffic', label: t('pages.cores.fidelity.trafficServed'), value: coreBytes(traffic) },
          ]} />
          {(inventory.error || actionErrors.length > 0) && <Alert type="warning" showIcon title={t('somethingWentWrong')} description={<>{inventory.error}{actionErrors.map((error) => <div key={error}>{error}</div>)}</>} />}
          {view === 'cards' ? <div className="p-grid core-card-grid">{rows.map((row) => <CoreCard key={row.core.id} {...row} series={trafficHistory[row.core.id]} restarting={restarting.includes(row.core.id)} onRestart={() => restart([row.core])} onOpen={(tab) => setDetail({ id: row.core.id, tab })} />)}</div>
            : <section className="p-card core-row-list"><Toolbar left={<span className="p-sub">{t(updates === undefined ? 'pages.cores.fidelity.countOnly' : 'pages.cores.fidelity.rowSummary', { count: cores.length, updates })}</span>}
              right={<Button size="small" icon={<Icon name="backup" size={14} />} onClick={exportStatus}>{t('pages.cores.fidelity.exportStatus')}</Button>} />
              {rows.map((row) => <CoreRow key={row.core.id} {...row} series={trafficHistory[row.core.id]} restarting={restarting.includes(row.core.id)} onRestart={() => restart([row.core])} onOpen={(tab) => setDetail({ id: row.core.id, tab })} />)}
            </section>}
        </div>}
      </Spin>
    </section>
    {selected && detail && <CoreDetailModal key={`${selected.id}:${detail.tab}`} core={selected} metrics={rows.find((row) => row.core.id === selected.id)?.metrics} series={trafficHistory[selected.id]} initialTab={detail.tab} onClose={() => setDetail(null)}
      onRestart={() => restart([selected])} restarting={restarting.includes(selected.id)} onInstalled={() => { setVersions({}); void refetch(); }} />}
  </ConfigProvider>;
}
