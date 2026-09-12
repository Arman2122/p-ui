import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Modal, Radio, Spin, Tag, Typography } from 'antd';

import { HttpUtil } from '@/utils';
import { CoreVersionsViewSchema } from '@/generated/zod';
import type { CoreStatusView, CoreVersionsView } from '@/generated/zod';
import { sameVersion } from '@/lib/cores/status';
import { coreMaintenanceKey, coreVersionLabel } from '@/lib/cores/display';

interface CoreVersionsModalProps {
  core: CoreStatusView | null;
  onClose: () => void;
  /* Called once an install landed, so the page re-reads what is on disk. */
  onInstalled: () => void;
}

/* Installing leaves the running binary unchanged until the operator restarts it. */
export function CoreVersionsPanel({ core, onInstalled }: { core: CoreStatusView; onInstalled: () => void }) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [view, setView] = useState<CoreVersionsView | null>(null);
  const [loading, setLoading] = useState(false);
  const [installing, setInstalling] = useState('');
  const [error, setError] = useState('');
  const coreId = core.id;
  const manageable = core.version.manageable;

  const load = useCallback(async (id: string) => {
    setLoading(true);
    setError('');
    try {
      const msg = await HttpUtil.get(`/panel/api/cores/${id}/versions`, undefined, { silentSuccess: true });
      if (msg?.success) {
        const parsed = CoreVersionsViewSchema.safeParse(msg.obj);
        setView(parsed.success ? parsed.data : null);
        if (!parsed.success) setError('somethingWentWrong');
      } else { setView(null); setError(msg?.msg || 'somethingWentWrong'); }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setView(null);
    if (manageable) void load(coreId);
  }, [coreId, manageable, load]);

  const install = useCallback((version: string) => {
    if (!core.version.manageable) return;
    const name = t(core.titleKey);
    modal.confirm({
      title: t('pages.cores.installConfirm', { core: name, version }),
      content: t('pages.cores.installNote'),
      okText: t('pages.cores.install'),
      cancelText: t('cancel'),
      onOk: async () => {
        setInstalling(version);
        try {
          const msg = await HttpUtil.post(`/panel/api/cores/${core.id}/install/${version}`);
          if (msg?.success) {
            onInstalled();
            await load(core.id);
          } else throw new Error(msg?.msg || t('somethingWentWrong'));
        } finally {
          setInstalling('');
        }
      },
    });
  }, [core, modal, t, onInstalled, load]);

  return (
    <>
      {modalContextHolder}
      <Spin spinning={loading || !!installing} tip={installing ? t('pages.index.dontRefresh') : undefined}>
        {error && <Alert type="error" showIcon title={t(error)} />}
        {view?.error && <Alert type="warning" showIcon className="mb-12" title={view.error} />}
        <Typography.Paragraph type="secondary">
          {t('pages.cores.installedVersion', { version: view?.installed || coreVersionLabel(core).text || t(coreVersionLabel(core).key!) })}
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary">{t(coreMaintenanceKey(core))}</Typography.Paragraph>
        {core.version.error && <Alert type="warning" showIcon title={core.version.error} />}
        {view && !view.manageable && <Alert type="info" showIcon title={t('pages.cores.reportOnly')} />}
        {core.version.manageable && <Button onClick={() => void load(core.id)} loading={loading}>{t('refresh')}</Button>}
        {view?.manageable && !loading && !view.available.length && <Alert type="info" showIcon title={t('pages.cores.noVersions')} />}
        <div className="core-version-list">
          {(view?.available ?? []).map((version) => (
            <div key={version} className="core-version-item">
              <Tag color={sameVersion(view?.installed, version) ? 'green' : 'purple'}>{version}</Tag>
              <Radio
                checked={sameVersion(view?.installed, version)}
                disabled={!view?.manageable || !!installing}
                aria-label={t('pages.cores.installConfirm', { core: core ? t(core.titleKey) : '', version })}
                onClick={() => install(version)}
              />
            </div>
          ))}
        </div>
      </Spin>
    </>
  );
}

export default function CoreVersionsModal({ core, onClose, onInstalled }: CoreVersionsModalProps) {
  const { t } = useTranslation();
  return <Modal open={!!core} title={core ? t('pages.cores.versionsOf', { core: t(core.titleKey) }) : ''} footer={null} onCancel={onClose}>
    {core && <CoreVersionsPanel key={core.id} core={core} onInstalled={onInstalled} />}
  </Modal>;
}
