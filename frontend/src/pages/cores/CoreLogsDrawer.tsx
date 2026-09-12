import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Drawer, Empty, Select, Space, Spin } from 'antd';
import { z } from 'zod';
import { SyncOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import type { CoreStatusView } from '@/generated/zod';

interface CoreLogsDrawerProps {
  core: CoreStatusView | null;
  onClose: () => void;
}

const LINE_CHOICES = [100, 200, 500, 1000];

/* The journal or exec ring retains lines across restarts, scoped to the selected instance. */
export function CoreLogsPanel({ core }: { core: CoreStatusView }) {
  const { t } = useTranslation();
  const [instance, setInstance] = useState(core.instances[0]?.instanceId ?? 0);
  const [n, setN] = useState(200);
  const [lines, setLines] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const defaultInstance = core.instances[0]?.instanceId ?? 0;
  const coreId = core.id;

  useEffect(() => {
    setLines([]);
    setInstance(defaultInstance);
  }, [coreId, defaultInstance]);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const msg = await HttpUtil.get<string[]>(`/panel/api/cores/${coreId}/logs`, { instance, n }, { silentSuccess: true });
      const parsed = z.array(z.string()).nullable().safeParse(msg?.obj);
      if (msg?.success && parsed.success) setLines(parsed.data ?? []);
      else { setLines([]); setError(msg?.msg || 'somethingWentWrong'); }
    } finally {
      setLoading(false);
    }
  }, [coreId, instance, n]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const instanceOptions = (core?.instances ?? []).map((i) => ({ value: i.instanceId, label: `${i.tag} (#${i.instanceId})` }));

  return (
    <>
      <Space wrap className="mb-12">
        {instanceOptions.length > 0 && (
          <Select value={instance} options={instanceOptions} onChange={setInstance} style={{ minWidth: 200 }} aria-label={t('pages.cores.instance')} />
        )}
        <Select value={n} options={LINE_CHOICES.map((v) => ({ value: v, label: t('pages.cores.lastLines', { count: v }) }))} onChange={setN} style={{ minWidth: 160 }} aria-label={t('pages.cores.lastLines', { count: n })} />
        <Button icon={<SyncOutlined />} onClick={refresh} loading={loading}>{t('refresh')}</Button>
      </Space>
      {error && <Alert type="error" showIcon title={t(error)} />}
      <Spin spinning={loading}>
        {lines.length === 0 ? (
          <Empty description={t('pages.cores.noLogs')} />
        ) : (
          <pre className="core-log">{lines.join('\n')}</pre>
        )}
      </Spin>
    </>
  );
}

export default function CoreLogsDrawer({ core, onClose }: CoreLogsDrawerProps) {
  const { t } = useTranslation();
  return <Drawer open={!!core} onClose={onClose} size={720} title={core ? t('pages.cores.logsOf', { core: t(core.titleKey) }) : ''}>
    {core && <CoreLogsPanel key={core.id} core={core} />}
  </Drawer>;
}
