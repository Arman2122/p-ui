import { useState } from 'react';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Descriptions, Form, Input, Modal, Space, Table, Tag, Tooltip, Typography, message } from 'antd';
import { useTranslation } from 'react-i18next';
import { FleetEnrollmentViewSchema, FleetNodePageSchema, FleetStatusViewSchema } from '@/generated/zod';
import { HttpUtil } from '@/utils';
import FleetNetwork from './FleetNetwork';

export default function HeadlessFleet() {
  const { t } = useTranslation();
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [network, setNetwork] = useState<{ node?: { id: string; name: string } }>();
  const [form] = Form.useForm<{ name: string }>();
  const [notice, contextHolder] = message.useMessage();
  const status = useQuery({
    queryKey: ['fleet', 'status'],
    queryFn: async () => {
      const response = await HttpUtil.get('/panel/api/fleet/status', undefined, { silent: true });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      return FleetStatusViewSchema.parse(response.obj);
    },
    refetchInterval: 10000,
  });
  const nodes = useInfiniteQuery({
    queryKey: ['fleet', 'nodes'],
    initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      const response = await HttpUtil.get('/panel/api/fleet/nodes', { after: pageParam, limit: 200 }, { silent: true });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      return FleetNodePageSchema.parse(response.obj);
    },
    getNextPageParam: (last) => last.nextCursor || undefined,
    refetchInterval: 10000,
  });

  const enroll = async ({ name }: { name: string }) => {
    setSaving(true);
    try {
      const response = await HttpUtil.post('/panel/api/fleet/enroll', { name });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      const enrollment = FleetEnrollmentViewSchema.parse(response.obj);
      const url = URL.createObjectURL(new Blob([enrollment.packageJson], { type: 'application/json' }));
      const link = document.createElement('a');
      link.href = url;
      link.download = `penhoon-enrollment-${enrollment.nodeId}.json`;
      document.body.append(link);
      link.click();
      link.remove();
      setTimeout(URL.revokeObjectURL.bind(URL, url), 1000);
      setOpen(false);
      form.resetFields();
      await cache.invalidateQueries({ queryKey: ['fleet', 'nodes'] });
      void notice.success(t('pages.nodes.fleet.downloaded'));
    } catch (error) {
      void notice.error(error instanceof Error ? error.message : t('somethingWentWrong'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card
      title={t('pages.nodes.fleet.title')}
      style={{ marginBottom: 16 }}
      extra={<Space wrap><Button onClick={() => setNetwork({})}>{t('pages.nodes.network.defaultsTitle')}</Button><Button type="primary" disabled={!status.data?.ready || status.data.recoveryRequired} onClick={() => setOpen(true)}>{t('pages.nodes.fleet.enroll')}</Button></Space>}
    >
      {contextHolder}
      <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
        {status.error && <Alert type="error" showIcon title={status.error.message} />}
        {status.data && !status.data.ready && <Alert type="warning" showIcon title={t('pages.nodes.fleet.notReady')} description={status.data.error} />}
        {status.data?.recoveryRequired && <Alert type="error" showIcon title={t('pages.nodes.fleet.recovery')} description={status.data.recoveryReason} />}
        {status.data?.ready && (
          <Descriptions size="small" column={1} items={[
            { key: 'address', label: t('pages.nodes.fleet.endpoint'), children: <Typography.Text copyable>{status.data.controlAddress}</Typography.Text> },
            { key: 'fingerprint', label: t('pages.nodes.fleet.fingerprint'), children: <Typography.Text code copyable style={{ overflowWrap: 'anywhere' }}>{status.data.rootSha256}</Typography.Text> },
          ]} />
        )}
        {nodes.error && <Alert type="error" showIcon title={nodes.error.message} />}
        <Table
          rowKey="id"
          loading={nodes.isPending}
          dataSource={nodes.data?.pages.flatMap(page => page.items) ?? []}
          scroll={{ x: 620 }}
          pagination={{ pageSize: 20, hideOnSinglePage: true }}
          columns={[
            { title: t('pages.nodes.fleet.name'), dataIndex: 'name', render: (_value, node) => <Space orientation="vertical" size={0}><Typography.Text strong>{node.name}</Typography.Text><Typography.Text type="secondary" copyable>{node.id}</Typography.Text></Space> },
            { title: t('pages.nodes.fleet.state'), render: (_value, node) => <Tag color={node.online ? 'success' : node.enrolled ? 'default' : 'processing'}>{node.online ? t('pages.nodes.fleet.online') : node.enrolled ? t('pages.nodes.fleet.offline') : t('pages.nodes.fleet.pending')}</Tag> },
            { title: t('pages.nodes.fleet.revision'), render: (_value, node) => <Space orientation="vertical" size={0}><Tag color={node.deploymentState === 'rejected' ? 'error' : node.appliedRevision === node.desiredRevision ? 'default' : 'warning'}>{node.appliedRevision} / {node.desiredRevision}</Tag>{node.deploymentState === 'rejected' && <Typography.Text type="danger">{t('pages.nodes.fleet.rejected')}</Typography.Text>}</Space> },
            { title: t('pages.nodes.fleet.runtime'), render: (_value, node) => <Tooltip title={<Space orientation="vertical" size={0}>{node.runtimeConditions.map(condition => <span key={condition.resourceId}>{condition.resourceId}: {condition.condition}</span>)}{node.runtimeObservedAt > 0 && <span>{new Date(node.runtimeObservedAt).toLocaleString()}</span>}</Space>}><Tag color={node.runtimeState === 'ready' ? 'success' : node.runtimeState === 'degraded' ? 'error' : 'default'}>{node.runtimeState === 'ready' ? t('pages.nodes.fleet.runtimeReady') : node.runtimeState === 'degraded' ? t('pages.nodes.fleet.runtimeFailed') : node.runtimeState === 'stale' ? t('pages.nodes.fleet.runtimeStale') : t('pages.nodes.fleet.runtimeUnknown')}</Tag></Tooltip> },
            { title: t('pages.nodes.fleet.lastSeen'), render: (_value, node) => node.lastSeenUnixMilli ? new Date(node.lastSeenUnixMilli).toLocaleString() : '—' },
            { title: t('pages.nodes.network.actions'), render: (_value, node) => <Button disabled={!node.enrolled} onClick={() => setNetwork({ node: { id: node.id, name: node.name } })}>{t('pages.nodes.network.networkTab')}</Button> },
          ]}
        />
        {nodes.hasNextPage && <Button loading={nodes.isFetchingNextPage} onClick={() => { void nodes.fetchNextPage(); }}>{t('pages.nodes.fleet.more')}</Button>}
      </Space>
      <Modal open={open} title={t('pages.nodes.fleet.enroll')} onCancel={() => setOpen(false)} onOk={() => form.submit()} confirmLoading={saving} destroyOnHidden>
        <Typography.Paragraph>{t('pages.nodes.fleet.enrollmentHint')}</Typography.Paragraph>
        <Form form={form} layout="vertical" onFinish={enroll}>
          <Form.Item name="name" label={t('pages.nodes.fleet.name')} rules={[{ required: true, whitespace: true }, { max: 128 }]}><Input autoComplete="off" maxLength={128} /></Form.Item>
        </Form>
      </Modal>
      {network && <FleetNetwork node={network.node} onClose={() => setNetwork(undefined)} />}
    </Card>
  );
}
