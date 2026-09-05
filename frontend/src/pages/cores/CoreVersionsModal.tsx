import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Modal, Radio, Spin, Tag, Typography } from 'antd';

import { HttpUtil } from '@/utils';
import { CoreVersionsViewSchema } from '@/generated/zod';
import type { CoreStatusView, CoreVersionsView } from '@/generated/zod';
import { sameVersion } from '@/lib/cores/status';

interface CoreVersionsModalProps {
  core: CoreStatusView | null;
  onClose: () => void;
  /* Called once an install landed, so the page re-reads what is on disk. */
  onInstalled: () => void;
}

/* The channel's releases for one core, the installed one marked. Picking one
   installs it and stops: the daemon keeps the old binary until a restart,
   which stays the operator's move and is said in the confirmation. */
export default function CoreVersionsModal({ core, onClose, onInstalled }: CoreVersionsModalProps) {
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [view, setView] = useState<CoreVersionsView | null>(null);
  const [loading, setLoading] = useState(false);
  const [installing, setInstalling] = useState('');

  const load = useCallback(async (id: string) => {
    setLoading(true);
    try {
      const msg = await HttpUtil.get(`/panel/api/cores/${id}/versions`, undefined, { silentSuccess: true });
      if (msg?.success) {
        const parsed = CoreVersionsViewSchema.safeParse(msg.obj);
        setView(parsed.success ? parsed.data : null);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setView(null);
    if (core) load(core.id);
  }, [core, load]);

  const install = useCallback((version: string) => {
    if (!core) return;
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
          }
        } finally {
          setInstalling('');
        }
      },
    });
  }, [core, modal, t, onInstalled, load]);

  return (
    <Modal open={!!core} title={core ? t('pages.cores.versionsOf', { core: t(core.titleKey) }) : ''} footer={null} onCancel={onClose}>
      {modalContextHolder}
      <Spin spinning={loading || !!installing} tip={installing ? t('pages.index.dontRefresh') : undefined}>
        {view?.error && <Alert type="warning" showIcon className="mb-12" title={view.error} />}
        <Typography.Paragraph type="secondary">
          {t('pages.cores.installedVersion', { version: view?.installed || t('pages.cores.notInstalled') })}
        </Typography.Paragraph>
        {view && !view.manageable && <Alert type="info" showIcon title={t('pages.cores.reportOnly')} />}
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
    </Modal>
  );
}
