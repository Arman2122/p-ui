import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Col, ConfigProvider, Layout, Modal, Result, Row, Space, Spin, Tooltip, Input, message } from 'antd';
import { FileTextOutlined, ReloadOutlined, TagsOutlined } from '@ant-design/icons';

import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import { setMessageInstance } from '@/utils/messageBus';
import { HttpUtil } from '@/utils';
import { CoreStateBadge } from '@/components/cores/CoreStateBadge';
import { summarizeCores } from '@/lib/cores/status';
import { coreColor, coreIcon, coreInstanceSummary, coreMaintenanceKey, coreRestarts, coreState, coreStateTone, coreVersionLabel } from '@/lib/cores/display';
import { StatStrip } from '@/components/ui';
import { PageHeader, PanelCard, Field } from '@/components/ui/panel';
import type { CoreStatusView } from '@/generated/zod';
import CoreVersionsModal from './CoreVersionsModal';
import CoreLogsDrawer from './CoreLogsDrawer';
import CoreDetailModal from './CoreDetailModal';
import { Icon } from '@/components/ui/Icon';
import './CoresPage.css';

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
  const visibleCores = cores.filter((c) => `${t(c.titleKey)} ${c.id} ${t(coreStateTone(c).labelKey)}`.toLowerCase().includes(query.toLowerCase()));

  const restart = useCallback((core: CoreStatusView) => {
    modal.confirm({
      title: t('pages.cores.restartConfirm', { core: t(core.titleKey) }),
      content: t('pages.cores.restartNote'),
      okText: t('pages.cores.restart'),
      cancelText: t('cancel'),
      onOk: async () => {
        setRestarting(core.id);
        try {
          const result = await HttpUtil.post(`/panel/api/cores/${core.id}/restart`);
          if (!result?.success) throw new Error(result?.msg || t('somethingWentWrong'));
          await refetch();
        } finally {
          setRestarting('');
        }
      },
    });
  }, [modal, t, refetch]);

  const summary = useMemo(() => summarizeCores(cores), [cores]);

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
          <section id="content-layout" className="content-area" aria-label={t('pages.cores.title')}>
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
                      {visibleCores.map((c) => {
                        const version = coreVersionLabel(c);
                        const instance = coreInstanceSummary(c);
                        return <div className="p-corerow" key={c.id} data-core-id={c.id}>
                        <span className="p-alert-i" style={{ color: coreColor(c.id), background: `color-mix(in srgb, ${coreColor(c.id)} 15%, transparent)` }}><Icon name={coreIcon(c.id)} /></span>
                        <div className="p-corerow-id">
                          <Space wrap size={8}>
                            <button type="button" className="p-text-action p-name" onClick={() => setSelectedId(c.id)}>{t(c.titleKey)}</button>
                            <CoreStateBadge state={coreState(c)} ready={c.ready} detail={c.unavailable || c.healthError || c.detail} />
                          </Space>
                          <div className="p-sub core-version">{c.id} · <Tooltip title={<>{t(coreMaintenanceKey(c))}{c.version.error && <div>{c.version.error}</div>}</>}>
                            <span className="p-mono">{version.text ?? t(version.key!)}</span>
                          </Tooltip></div>
                          {(c.healthError || c.unavailable) && <div className="p-sub core-unavailable">{c.unavailable || c.healthError}</div>}
                          {c.trafficError && <div className="p-sub">{t('pages.cores.trafficError')}: {c.trafficError}</div>}
                        </div>
                        <div className="p-corerow-stats">
                          <Field label={t(instance.labelKey)} value={instance.valueKey ? t(instance.valueKey) : instance.value} />
                          <Field label={t('pages.cores.columns.restarts')} value={coreRestarts(c) ?? '—'} />
                        </div>
                        <div className="p-core-row-actions" role="group" aria-label={t('pages.cores.columns.actions')}>
                          <Button size="small" onClick={() => setSelectedId(c.id)}>{t('pages.cores.columns.detail')}</Button>
                          <Tooltip title={!c.available ? c.unavailable : undefined}><span><Button size="small" aria-label={t('pages.cores.restart')} icon={<ReloadOutlined aria-hidden />} disabled={!c.available} loading={restarting === c.id} onClick={() => restart(c)}>{t('pages.cores.restart')}</Button></span></Tooltip>
                          {c.logs && <Button size="small" aria-label={t('pages.cores.logs')} icon={<FileTextOutlined aria-hidden />} onClick={() => setLogsFor(c)}>{t('pages.cores.logs')}</Button>}
                          {c.version.manageable && <Button size="small" aria-label={t('pages.cores.versions')} icon={<TagsOutlined aria-hidden />} onClick={() => setVersionsFor(c)}>{t('pages.cores.versions')}</Button>}
                        </div>
                      </div>; })}
                      {!visibleCores.length && <div className="p-plan-empty">{t('noData')}</div>}
                    </PanelCard>
                  </Col>
                </Row>
              )}
            </Spin>
          </section>
        </Layout>
      </Layout>
      {selected && <CoreDetailModal key={selected.id} core={selected} onClose={() => setSelectedId(null)} onRestart={() => restart(selected)} restarting={restarting === selected.id} onInstalled={() => void refetch()} />}
      <CoreVersionsModal core={versionsFor} onClose={() => setVersionsFor(null)} onInstalled={() => refetch()} />
      <CoreLogsDrawer core={logsFor} onClose={() => setLogsFor(null)} />
    </ConfigProvider>
  );
}
