import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Col, ConfigProvider, Layout, Modal, Result, Row, Space, Spin, Statistic, Table, Tag, Tooltip, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CheckCircleOutlined, DeploymentUnitOutlined, FileTextOutlined, ReloadOutlined, StopOutlined, TagsOutlined, WarningOutlined } from '@ant-design/icons';

import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import AppSidebar from '@/layouts/AppSidebar';
import { setMessageInstance } from '@/utils/messageBus';
import { HttpUtil } from '@/utils';
import { CoreStateTag } from '@/components/cores/CoreStateTag';
import { summarizeCores } from '@/lib/cores/status';
import type { CoreStatusView, InstanceStatusView } from '@/generated/zod';
import CoreVersionsModal from './CoreVersionsModal';
import CoreLogsDrawer from './CoreLogsDrawer';
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
    { title: t('pages.cores.columns.state'), key: 'state', render: (_, i) => <CoreStateTag state={i.state} ready={i.ready} detail={i.detail} /> },
    { title: 'PID', dataIndex: 'pid', key: 'pid', render: (pid?: number) => pid || '' },
    { title: t('pages.cores.columns.restarts'), dataIndex: 'restarts', key: 'restarts', render: (n?: number) => n ?? 0 },
    { title: t('pages.cores.columns.since'), dataIndex: 'since', key: 'since', render: (ms?: number) => when(ms) },
    { title: t('pages.cores.columns.detail'), dataIndex: 'detail', key: 'detail', render: (d?: string) => d ? <Tooltip title={d}><span className="core-detail">{d}</span></Tooltip> : '' },
  ], [t]);

  const columns = useMemo<ColumnsType<CoreStatusView>>(() => [
    {
      title: t('pages.cores.columns.core'),
      key: 'core',
      render: (_, c) => (
        <div className="core-title">
          <Typography.Text strong>{t(c.titleKey)}</Typography.Text>
          <span className="core-id">{c.id}</span>
        </div>
      ),
    },
    {
      title: t('pages.cores.columns.state'),
      key: 'state',
      render: (_, c) => c.available ? (
        <Space direction="vertical" size={2}>
          <CoreStateTag state={c.state} ready={c.ready} detail={c.detail || c.healthError} />
          {(c.detail || c.healthError) && <span className="core-detail">{c.detail || c.healthError}</span>}
        </Space>
      ) : (
        <Tooltip title={c.unavailable}><Tag icon={<StopOutlined />}>{t('pages.cores.unavailable')}</Tag></Tooltip>
      ),
    },
    {
      title: t('pages.cores.columns.version'),
      key: 'version',
      render: (_, c) => {
        if (!c.version.reported) return <span className="core-id">—</span>;
        return (
          <Space size={4} wrap>
            <Tag>{c.version.installed || t('pages.cores.notInstalled')}</Tag>
            {c.version.manageable ? (
              <Button size="small" icon={<TagsOutlined />} onClick={() => setVersionsFor(c)}>{t('pages.cores.versions')}</Button>
            ) : (
              <Tooltip title={t('pages.cores.reportOnly')}><span className="core-id">{t('pages.cores.reportOnlyShort')}</span></Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: t('pages.cores.columns.instances'),
      key: 'instances',
      render: (_, c) => c.instances.length > 0 ? c.instances.length : (c.pid ? `PID ${c.pid}` : ''),
    },
    {
      title: t('pages.cores.columns.traffic'),
      key: 'traffic',
      render: (_, c) => c.trafficError
        ? <Tooltip title={c.trafficError}><Tag color="error" icon={<WarningOutlined />}>{t('pages.cores.trafficError')}</Tag></Tooltip>
        : (c.trafficSeenAt ? when(c.trafficSeenAt) : <span className="core-id">{t('pages.cores.trafficNever')}</span>),
    },
    {
      title: t('pages.cores.columns.actions'),
      key: 'actions',
      render: (_, c) => (
        <Space wrap>
          <Button size="small" icon={<ReloadOutlined />} loading={restarting === c.id} disabled={!c.available} onClick={() => restart(c)}>{t('pages.cores.restart')}</Button>
          {c.logs && <Button size="small" icon={<FileTextOutlined />} onClick={() => setLogsFor(c)}>{t('pages.cores.logs')}</Button>}
        </Space>
      ),
    },
  ], [t, restarting, restart]);

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
        <AppSidebar />
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
                    <Card size="small" hoverable className="summary-card">
                      <Row gutter={[16, 12]}>
                        <Col xs={12} sm={6}>
                          <Statistic title={t('pages.cores.summary.total')} value={String(summary.total)} prefix={<DeploymentUnitOutlined />} />
                        </Col>
                        <Col xs={12} sm={6}>
                          <Statistic title={t('pages.cores.summary.healthy')} value={String(summary.healthy)} prefix={<CheckCircleOutlined style={{ color: 'var(--ant-color-success)' }} />} />
                        </Col>
                        <Col xs={12} sm={6}>
                          <Statistic title={t('pages.cores.summary.attention')} value={String(summary.attention)} prefix={<WarningOutlined style={{ color: summary.attention ? 'var(--ant-color-warning)' : undefined }} />} />
                        </Col>
                        <Col xs={12} sm={6}>
                          <Statistic title={t('pages.cores.summary.unavailable')} value={String(summary.unavailable)} prefix={<StopOutlined />} />
                        </Col>
                      </Row>
                    </Card>
                  </Col>
                  <Col span={24}>
                    <Card
                      size="small"
                      title={t('pages.cores.title')}
                      extra={<Button icon={<ReloadOutlined />} loading={loading} onClick={() => refetch()}>{t('refresh')}</Button>}
                    >
                      <Table<CoreStatusView>
                        rowKey="id"
                        size="small"
                        columns={columns}
                        dataSource={cores}
                        pagination={false}
                        scroll={{ x: true }}
                        expandable={{
                          rowExpandable: (c) => c.instances.length > 0,
                          expandedRowRender: (c) => (
                            <Table<InstanceStatusView> rowKey="instanceId" size="small" columns={instanceColumns} dataSource={c.instances} pagination={false} />
                          ),
                        }}
                      />
                    </Card>
                  </Col>
                </Row>
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
      <CoreVersionsModal core={versionsFor} onClose={() => setVersionsFor(null)} onInstalled={() => refetch()} />
      <CoreLogsDrawer core={logsFor} onClose={() => setLogsFor(null)} />
    </ConfigProvider>
  );
}
