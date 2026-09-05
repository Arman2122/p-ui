import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Drawer, Empty, Select, Space, Spin } from 'antd';
import { SyncOutlined } from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import type { CoreStatusView } from '@/generated/zod';

interface CoreLogsDrawerProps {
  core: CoreStatusView | null;
  onClose: () => void;
}

const LINE_CHOICES = [100, 200, 500, 1000];

/* The daemon's last lines, per inbound for a core that runs one each: the
   journal under systemd, the panel's own ring under exec, kept across the
   restart that repaired a crash — which is when an operator opens this. */
export default function CoreLogsDrawer({ core, onClose }: CoreLogsDrawerProps) {
  const { t } = useTranslation();
  const [instance, setInstance] = useState(0);
  const [n, setN] = useState(200);
  const [lines, setLines] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    setLines([]);
    setInstance(core?.instances[0]?.instanceId ?? 0);
  }, [core]);

  const refresh = useCallback(async () => {
    if (!core) return;
    setLoading(true);
    try {
      const msg = await HttpUtil.get<string[]>(`/panel/api/cores/${core.id}/logs`, { instance, n }, { silentSuccess: true });
      if (msg?.success) setLines(msg.obj ?? []);
    } finally {
      setLoading(false);
    }
  }, [core, instance, n]);

  useEffect(() => {
    if (core) refresh();
  }, [core, instance, n, refresh]);

  const instanceOptions = (core?.instances ?? []).map((i) => ({ value: i.instanceId, label: `${i.tag} (#${i.instanceId})` }));

  return (
    <Drawer open={!!core} onClose={onClose} width={720} title={core ? t('pages.cores.logsOf', { core: t(core.titleKey) }) : ''}>
      <Space wrap className="mb-12">
        {instanceOptions.length > 0 && (
          <Select value={instance} options={instanceOptions} onChange={setInstance} style={{ minWidth: 200 }} aria-label={t('pages.cores.instance')} />
        )}
        <Select value={n} options={LINE_CHOICES.map((v) => ({ value: v, label: t('pages.cores.lastLines', { count: v }) }))} onChange={setN} style={{ minWidth: 160 }} aria-label={t('pages.cores.lastLines', { count: n })} />
        <Button icon={<SyncOutlined />} onClick={refresh} loading={loading}>{t('refresh')}</Button>
      </Space>
      <Spin spinning={loading}>
        {lines.length === 0 ? (
          <Empty description={t('pages.cores.noLogs')} />
        ) : (
          <pre className="core-log">{lines.join('\n')}</pre>
        )}
      </Spin>
    </Drawer>
  );
}
