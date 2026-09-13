import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, Spin, Tag, Typography } from 'antd';

import { HttpUtil } from '@/utils';
import { CoreVersionsViewSchema } from '@/generated/zod';
import type { CoreStatusView, CoreVersionsView } from '@/generated/zod';
import { sameVersion } from '@/lib/cores/status';
import { coreCanRestart, coreHasOtherProcesses, coreHostProcesses, coreMaintenanceKey, coreNeedsRestart, coreVersionContextKey, coreVersionLabel, isBuiltinCore } from '@/lib/cores/display';

interface CoreVersionsModalProps {
  core: CoreStatusView | null;
  onClose: () => void;
  /* Called once an install landed, so the page re-reads what is on disk. */
  onInstalled: () => void;
  onRestart?: () => void;
  restarting?: boolean;
}

/* Reinstalling can replace an executable without changing its version string;
   process identity is the local acknowledgement until the host probe refreshes. */
function runningManagedPids(core: CoreStatusView): number[] {
  return [...new Set([
    ...(core.state === 'running' && core.pid ? [core.pid] : []),
    ...core.instances.filter((instance) => instance.state === 'running' && instance.pid).map((instance) => instance.pid!),
    ...coreHostProcesses(core).filter((process) => process.managed && process.state === 'running').map((process) => process.pid),
  ])];
}

/* Installing leaves the running binary unchanged until the operator restarts it. */
export function CoreVersionsPanel({ core, onInstalled, onRestart, restarting = false }: {
  core: CoreStatusView; onInstalled: () => void; onRestart?: () => void; restarting?: boolean;
}) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [view, setView] = useState<CoreVersionsView | null>(null);
  const [loading, setLoading] = useState(false);
  const [installing, setInstalling] = useState('');
  const [selected, setSelected] = useState('');
  const [refreshError, setRefreshError] = useState('');
  const [installError, setInstallError] = useState<{ version: string; message: string } | null>(null);
  const [installedVersion, setInstalledVersion] = useState<string>();
  const [installedRelease, setInstalledRelease] = useState('');
  const [installedPids, setInstalledPids] = useState<number[]>([]);
  const latestCore = useRef(core);
  latestCore.current = core;
  const generation = useRef(0);
  const loadSequence = useRef(0);
  const phase = useRef<'idle' | 'confirm' | 'install'>('idle');
  const confirmation = useRef<ReturnType<typeof modal.confirm> | null>(null);
  const coreId = core.id;
  const manageable = core.version.manageable && !isBuiltinCore(core);

  const load = useCallback(async (id: string, epoch = generation.current) => {
    const sequence = ++loadSequence.current;
    const active = () => epoch === generation.current && sequence === loadSequence.current;
    setLoading(true);
    setRefreshError('');
    try {
      const msg = await HttpUtil.get(`/panel/api/cores/${id}/versions`, undefined, { silentSuccess: true });
      if (!active()) return;
      if (!msg?.success) throw new Error(msg?.msg || 'coreUpdates.refreshFailed');
      const parsed = CoreVersionsViewSchema.safeParse(msg.obj);
      if (!parsed.success) throw new Error('coreUpdates.invalidResponse');
      if (parsed.data.installed) setInstalledVersion(parsed.data.installed);
      if (parsed.data.error) throw new Error(parsed.data.error);
      setView(parsed.data);
    } catch (cause) {
      if (active()) setRefreshError(cause instanceof Error ? cause.message : 'coreUpdates.refreshFailed');
    } finally {
      if (active()) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const epoch = ++generation.current;
    setView(null);
    setInstalledVersion(undefined);
    setInstalledRelease('');
    setInstalledPids([]);
    setInstallError(null);
    setRefreshError('');
    setSelected('');
    setInstalling('');
    setLoading(false);
    phase.current = 'idle';
    if (manageable) void load(coreId, epoch);
    return () => {
      generation.current += 1;
      loadSequence.current += 1;
      confirmation.current?.destroy();
    };
  }, [coreId, manageable, load]);

  const install = useCallback((version: string) => {
    if (!manageable || !view?.manageable || loading || restarting || phase.current !== 'idle') return;
    phase.current = 'confirm';
    setSelected(version);
    const epoch = generation.current;
    const active = () => epoch === generation.current;
    const name = t(core.titleKey);
    confirmation.current = modal.confirm({
      title: t('pages.cores.installConfirm', { core: name, version }),
      content: t('pages.cores.installNote'),
      okText: t('pages.cores.install'),
      cancelText: t('cancel'),
      keyboard: false,
      onCancel: () => { if (active() && phase.current === 'confirm') { phase.current = 'idle'; setSelected(''); } },
      onOk: async () => {
        if (!active() || phase.current !== 'confirm') return;
        phase.current = 'install';
        const capturedPids = runningManagedPids(latestCore.current);
        loadSequence.current += 1;
        confirmation.current?.update({ cancelButtonProps: { disabled: true } });
        setInstalling(version);
        setInstallError(null);
        let acknowledged = false;
        try {
          const msg = await HttpUtil.post(`/panel/api/cores/${encodeURIComponent(core.id)}/install/${encodeURIComponent(version)}`);
          if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
          acknowledged = true;
          if (active()) { setInstalledVersion(version); setInstalledRelease(version); setInstalledPids(capturedPids); }
          onInstalled();
          if (active()) void load(core.id, epoch);
        } catch (cause) {
          if (active()) {
            const message = cause instanceof Error ? cause.message : t('somethingWentWrong');
            if (acknowledged) setRefreshError(message);
            else setInstallError({ version, message });
          }
        } finally {
          if (active()) { phase.current = 'idle'; setInstalling(''); setSelected(''); }
        }
      },
    });
  }, [core, manageable, view?.manageable, loading, restarting, modal, t, onInstalled, load]);

  const installed = installedVersion || view?.installed || coreVersionLabel(core).text || t(coreVersionLabel(core).key!);
  const managedRunning = core.state === 'running' || core.instances.some((instance) => instance.state === 'running');
  const currentPids = runningManagedPids(core);
  const needsRestart = coreNeedsRestart(core) || installedPids.some((pid) => currentPids.includes(pid));
  const busy = loading || !!selected || !!installing || restarting;

  return (
    <>
      {modalContextHolder}
      {installError && <Alert type="error" showIcon title={t('coreUpdates.installFailed', { version: installError.version })} description={installError.message} style={{ marginBottom: 12 }} />}
      {installedRelease && <Alert type="success" showIcon title={t('coreUpdates.installSucceeded', { version: installedRelease })} style={{ marginBottom: 12 }} description={managedRunning ? undefined : t('coreUpdates.nextStart')} />}
      {needsRestart && <Alert type="warning" showIcon title={t('coreUpdates.restartRequired')} style={{ marginBottom: 12 }} description={<>
        <p>{t('coreUpdates.runningNeedsRestart')}</p>
        {onRestart && coreCanRestart(core) && <Button aria-label={t('coreUpdates.restartManaged')} disabled={!!selected || !!installing || restarting} loading={restarting}
          onClick={() => { if (!selected && !installing && !restarting && coreCanRestart(core)) onRestart(); }}>{t('coreUpdates.restartManaged')}</Button>}
      </>} />}
      {(installedRelease || needsRestart) && coreHasOtherProcesses(core) && <Alert type="info" showIcon title={t('coreUpdates.externalUnaffected')} style={{ marginBottom: 12 }} />}
      {refreshError && <Alert type={installedRelease ? 'warning' : 'error'} showIcon title={t(installedRelease ? 'coreUpdates.refreshAfterInstallFailed' : 'coreUpdates.refreshFailed')} description={t(refreshError)} style={{ marginBottom: 12 }} />}
      <Spin spinning={loading || !!installing} description={installing ? t('coreUpdates.installingVersion', { version: installing }) : undefined}>
        <Typography.Paragraph type="secondary" style={{ overflowWrap: 'anywhere' }}>
          {t(manageable ? 'coreUpdates.installedOnDisk' : 'pages.cores.installedVersion', { version: installed })}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary">{t(coreMaintenanceKey(core))}</Typography.Paragraph>
        <Typography.Paragraph type="secondary">{t(coreVersionContextKey(core))}</Typography.Paragraph>
        {core.version.error && <Alert type="warning" showIcon title={core.version.error} />}
        {view && !view.manageable && <Alert type="info" showIcon title={t('pages.cores.reportOnly')} />}
        {manageable && <Button aria-label={t('refresh')} onClick={() => void load(core.id)} loading={loading} disabled={!!selected || !!installing || restarting}>{t('refresh')}</Button>}
        {view?.manageable && !loading && !refreshError && !view.available.length && <Alert type="info" showIcon title={t('pages.cores.noVersions')} />}
        <div className="core-version-list">
          {(manageable ? view?.available ?? [] : []).map((version) => (
            <div key={version} className="core-version-item">
              <Tag style={{ color: 'var(--p-text)', borderColor: sameVersion(installed, version) ? 'var(--p-primary)' : undefined }}>{version}</Tag>
              <Button size="small"
                disabled={!manageable || !view?.manageable || busy}
                aria-label={t('pages.cores.installConfirm', { core: core ? t(core.titleKey) : '', version })}
                onClick={() => install(version)}
              >{t(sameVersion(installed, version) ? 'coreUpdates.reinstall' : 'pages.cores.install')}</Button>
            </div>
          ))}
        </div>
      </Spin>
    </>
  );
}

export default function CoreVersionsModal({ core, onClose, onInstalled, onRestart, restarting }: CoreVersionsModalProps) {
  const { t } = useTranslation();
  return <Modal open={!!core} title={core ? t('pages.cores.versionsOf', { core: t(core.titleKey) }) : ''} footer={null} onCancel={onClose}>
    {core && <CoreVersionsPanel key={core.id} core={core} onInstalled={onInstalled} onRestart={onRestart} restarting={restarting} />}
  </Modal>;
}
