import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Col, ConfigProvider, Layout, Modal, Result, Row, Space, Spin, Table, Tooltip, Tabs, Input, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { FileTextOutlined, ReloadOutlined, TagsOutlined } from '@ant-design/icons';

import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import { setMessageInstance } from '@/utils/messageBus';
import { HttpUtil } from '@/utils';
import { CoreStateBadge } from '@/components/cores/CoreStateBadge';
import { summarizeCores } from '@/lib/cores/status';
import { StatStrip } from '@/components/ui';
import { PageHeader, PanelCard, Field } from '@/components/ui/panel';
import type { CoreStatusView, InstanceStatusView } from '@/generated/zod';
import CoreVersionsModal from './CoreVersionsModal';
import CoreLogsDrawer from './CoreLogsDrawer';
import { Icon } from '@/components/ui/Icon';
import './CoresPage.css';

function when(ms?: number): string {
  return ms ? new Date(ms).toLocaleString() : '';
}

export default function CoresPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => { setMessageInstance(messageApi); }, [messageApi]);

  const { cores, loading, fetched, fetchError, refetch } = useCoresStatusQuery();
  const [versionsFor, setVersionsFor] = useState<CoreStatusView | null>(null);
  const [logsFor, setLogsFor] = useState<CoreStatusView | null>(null);
  const [restarting, setRestarting] = useState('');
  const [query, setQuery] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = cores.find((c) => c.id === selectedId);
  const visibleCores = cores.filter((c) => `${t(c.titleKey)} ${c.id} ${c.state}`.toLowerCase().includes(query.toLowerCase()));

  const restart = useCallback((core: CoreStatusView) => {
    modal.confirm({
      title: t('pages.cores.restartConfirm', { core: t(core.titleKey) }),
      content: t('pages.cores.restartNote'),
      okText: t('pages.cores.restart'),
      cancelText: t('cancel'),
      onOk: async () => {
        setRestarting(core.id);
        try {
          await HttpUtil.post(`/panel/api/cores/${core.id}/restart`);
          await refetch();
        } finally {
          setRestarting('');
        }
      },
    });
  }, [modal, t, refetch]);

  const summary = useMemo(() => summarizeCores(cores), [cores]);

  const instanceColumns = useMemo<ColumnsType<InstanceStatusView>>(() => [
    { title: t('pages.cores.columns.instance'), dataIndex: 'tag', key: 'tag', render: (tag: string, i) => <span>{tag} <span className="core-id">#{i.instanceId}</span></span> },
    { title: t('pages.cores.columns.state'), key: 'state', render: (_, i) => <CoreStateBadge state={i.state} ready={i.ready} detail={i.detail} /> },
    { title: 'PID', dataIndex: 'pid', key: 'pid', render: (pid?: number) => pid || '' },
    { title: t('pages.cores.columns.restarts'), dataIndex: 'restarts', key: 'restarts', render: (n?: number) => n ?? 0 },
    { title: t('pages.cores.columns.since'), dataIndex: 'since', key: 'since', render: (ms?: number) => when(ms) },
    { title: t('pages.cores.columns.detail'), dataIndex: 'detail', key: 'detail', render: (d?: string) => d ? <Tooltip title={d}><span className="core-detail">{d}</span></Tooltip> : '' },
  ], [t]);

  const pageClass = useMemo(() => {
    const classes = ['cores-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      {modalContextHolder}
      <Layout className={pageClass}>
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Spin spinning={!fetched} delay={200} size="large">
              {!fetched ? (
                <div className="loading-spacer" />
              ) : fetchError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={fetchError}
                  extra={<Button type="primary" loading={loading} onClick={() => refetch()}>{t('refresh')}</Button>}
                />
              ) : (
                <Row gutter={[isMobile ? 8 : 16, isMobile ? 8 : 12]}>
                  <Col span={24}>
                    <PageHeader
                      title={t('pages.cores.title')}
                      sub={t('pages.cores.subtitle')}
                      actions={<Button icon={<ReloadOutlined />} loading={loading} onClick={() => refetch()}>{t('refresh')}</Button>}
                    />
                  </Col>

                  <Col span={24}>
                    <StatStrip
                      items={[
                        { key: 'total', label: t('pages.cores.summary.total'), value: summary.total },
                        { key: 'healthy', label: t('pages.cores.summary.healthy'), value: summary.healthy, color: 'var(--p-success)', pulse: summary.healthy > 0 },
                        { key: 'attention', label: t('pages.cores.summary.attention'), value: summary.attention, color: summary.attention ? 'var(--p-warning)' : undefined },
                        { key: 'unavailable', label: t('pages.cores.summary.unavailable'), value: summary.unavailable, color: '#8b8d94' },
                      ]}
                    />
                  </Col>
                  <Col span={24}>
                    <PanelCard pad={0} className="p-core-list">
                      <div style={{ padding: 12 }}><Input.Search value={query} onChange={(e) => setQuery(e.target.value)}
                        allowClear aria-label={t('menu.cores')} placeholder={t('menu.cores')} /></div>
                      {visibleCores.map((c) => <div className="p-corerow" key={c.id}>
                        <span className="p-alert-i"><Icon name="xray" /></span>
                        <div className="p-corerow-id">
                          <Space wrap size={8}>
                            <button className="p-text-action p-name" onClick={() => setSelectedId(c.id)}>{t(c.titleKey)}</button>
                            <CoreStateBadge state={c.state} ready={c.ready} detail={c.detail || c.healthError || c.unavailable} />
                          </Space>
                          <div className="p-sub" style={{ marginTop: 4 }}>{c.id} · <span className="p-mono">{c.version.installed || t('pages.cores.notInstalled')}</span></div>
                          {(c.healthError || c.unavailable) && <div className="p-sub">{!c.available ? `${t('pages.cores.unavailable')}: ` : ''}{c.healthError || c.unavailable}</div>}
                          {c.trafficError && <div className="p-sub">{t('pages.cores.trafficError')}: {c.trafficError}</div>}
                        </div>
                        <div className="p-corerow-stats">
                          <Field label={t('pages.cores.columns.instances')} value={c.instances.length} />
                          <Field label={t('pages.cores.columns.restarts')} value={c.restarts ?? c.instances.reduce((n, i) => n + (i.restarts ?? 0), 0)} />
                        </div>
                        <div className="p-core-row-actions" role="group" aria-label={t('pages.cores.columns.actions')}>
                          <Button size="small" onClick={() => setSelectedId(c.id)}>{t('pages.cores.columns.detail')}</Button>
                          <Button size="small" icon={<ReloadOutlined />} disabled={!c.available} loading={restarting === c.id} onClick={() => restart(c)}>{t('pages.cores.restart')}</Button>
                          {c.logs && <Button size="small" icon={<FileTextOutlined />} onClick={() => setLogsFor(c)}>{t('pages.cores.logs')}</Button>}
                          {c.version.manageable && <Button size="small" icon={<TagsOutlined />} onClick={() => setVersionsFor(c)}>{t('pages.cores.versions')}</Button>}
                        </div>
                      </div>)}
                      {!visibleCores.length && <div className="p-plan-empty">{t('noData')}</div>}
                    </PanelCard>
                  </Col>
                </Row>
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
      <Modal open={!!selected} title={selected ? t(selected.titleKey) : ''} width={900} footer={null} onCancel={() => setSelectedId(null)}>
        {selected && <Tabs items={[
          { key: 'overview', label: t('menu.dashboard'), children: <>
            <div className="p-detail-grid" style={{ padding: '12px 0' }}>
              <Field label={t('pages.cores.columns.core')} value={selected.id} />
              <Field label={t('pages.cores.columns.state')} value={<CoreStateBadge state={selected.state} ready={selected.ready} detail={selected.detail} />} />
              <Field label={t('pages.cores.columns.version')} value={selected.version.installed || t('pages.cores.notInstalled')} />
              <Field label="PID" value={selected.pid || '—'} />
              <Field label={t('pages.cores.columns.since')} value={when(selected.since) || '—'} />
              <Field label={t('pages.cores.columns.traffic')} value={selected.trafficSeenAt ? when(selected.trafficSeenAt) : t('pages.cores.trafficNever')} />
            </div>
            {[selected.detail, selected.healthError, selected.trafficError, selected.unavailable, selected.version.error].filter(Boolean).map((detail, i) => <p className="p-sub" key={i}>{detail}</p>)}
            {!selected.version.manageable && <p className="p-sub"><b>{t('pages.cores.reportOnlyShort')}</b> · {t('pages.cores.reportOnly')}</p>}
          </> },
          { key: 'instances', label: t('pages.cores.columns.instances'), children: <Table<InstanceStatusView> className="p-table" rowKey="instanceId" size="small"
            columns={instanceColumns} dataSource={selected.instances} pagination={false} scroll={{ x: 650 }} /> },
        ]} />}
      </Modal>
      <CoreVersionsModal core={versionsFor} onClose={() => setVersionsFor(null)} onInstalled={() => refetch()} />
      <CoreLogsDrawer core={logsFor} onClose={() => setLogsFor(null)} />
    </ConfigProvider>
  );
}
