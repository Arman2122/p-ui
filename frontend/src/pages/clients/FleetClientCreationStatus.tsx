import { Alert, Button, Popconfirm, Space, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { FleetClientCreation } from './useFleetClientCreation';

export function FleetClientCreationStatus({ creation, onStartNew, onComplete }: { creation: FleetClientCreation; onStartNew: () => void; onComplete: () => void }) {
  const { t } = useTranslation();
  const active = creation.activeOperationId;
  const earlier = creation.operations.filter(id => id !== active);
  const notice = creation.notice === 'storage' ? t('pages.clients.creation.storageUnavailable')
    : creation.notice === 'invalid' ? t('pages.clients.creation.invalid')
    : creation.notice === 'receiptUnavailable' ? t('pages.clients.creation.receiptUnavailable') : null;
  return <Space orientation="vertical" style={{ width: '100%', marginBottom: 16 }}>
    {creation.mode === 'unavailable' && <Alert type="warning" title={t('pages.clients.creation.unavailable')} description={notice}
      action={<Button icon={<ReloadOutlined />} loading={creation.loading} onClick={() => { void creation.refreshCapability(); }}>{t('refresh')}</Button>} />}
    {creation.mode !== 'unavailable' && notice && <Alert type="warning" title={notice} />}
    {creation.diagnostic && <Typography.Text type="secondary">{t('pages.clients.creation.latestResponse', { message: creation.diagnostic })}</Typography.Text>}
    {active && <Alert type="warning" title={t('pages.clients.creation.unconfirmed')}
      description={<Space orientation="vertical" style={{ width: '100%' }}>
        <span>{t(creation.memory ? 'pages.clients.creation.exactRetryHint' : 'pages.clients.creation.receiptOnlyHint')}</span>
        <Typography.Text code dir="ltr">{active}</Typography.Text>
        <Space wrap>
          {creation.memory && <Button aria-label={t('pages.clients.creation.retryExact')} aria-busy={creation.busy} disabled={creation.busy || creation.mode !== 'fleet'} loading={creation.busy} onClick={() => { void creation.retry().then(result => { if (result.success) onComplete(); }); }}>{t('pages.clients.creation.retryExact')}</Button>}
          <Button aria-label={t('pages.clients.creation.checkReceipt')} aria-busy={creation.busy} disabled={creation.busy} loading={creation.busy} onClick={() => { void creation.checkReceipt(active).then(result => { if (result) onComplete(); }); }}>{t('pages.clients.creation.checkReceipt')}</Button>
          <Popconfirm title={t('pages.clients.creation.startNewTitle')} description={t('pages.clients.creation.startNewConfirm')}
            okText={t('pages.clients.creation.startNew')} cancelText={t('cancel')} onConfirm={onStartNew}>
            <Button disabled={creation.busy || creation.mode !== 'fleet'}>{t('pages.clients.creation.startNew')}</Button>
          </Popconfirm>
        </Space>
      </Space>} />}
    {creation.result && <Alert type="success" title={t(creation.result.pending ? 'pages.clients.creation.savedPending' : 'pages.clients.creation.saved')}
      description={creation.result.nodeDerived.length ? t('pages.clients.creation.nodeCredentialsPending') : undefined} />}
    {earlier.length > 0 && <Alert type="info" title={t('pages.clients.creation.earlierRequests')}
      description={<Space orientation="vertical" style={{ width: '100%' }}>{earlier.map(id => <Space key={id} wrap>
        <Typography.Text code dir="ltr">{id}</Typography.Text>
        <Button size="small" loading={creation.busy} onClick={() => { void creation.checkReceipt(id); }} aria-label={t('pages.clients.creation.checkEarlierReceipt', { id })}>{t('pages.clients.creation.checkReceipt')}</Button>
      </Space>)}</Space>} />}
  </Space>;
}
