import { Button, Drawer, Empty, Space, Table, Tag, Typography, message } from 'antd';
import { useTranslation } from 'react-i18next';

import type { BulkOutcome, BulkRow } from '@/schemas/client';

export interface BulkResultDrawerProps {
  open: boolean;
  title: string;
  outcome: BulkOutcome | null;
  onClose: () => void;
  onRetry?: (emails: string[]) => void;
}

/* Every bulk run ends here when a row was skipped or failed: the toast keeps
   the count, this lists each row with its reason and offers a retry. */
export default function BulkResultDrawer({ open, title, outcome, onClose, onRetry }: BulkResultDrawerProps) {
  const { t } = useTranslation();
  const ok = outcome?.ok ?? [];
  const skipped = outcome?.skipped ?? [];
  const failed = outcome?.failed ?? [];

  const columns = [
    { title: t('pages.clients.bulkResult.email'), dataIndex: 'email', key: 'email', width: '40%' },
    { title: t('pages.clients.bulkResult.reason'), dataIndex: 'reason', key: 'reason' },
  ];

  async function copyFailed() {
    const text = failed.map((r) => r.email).join('\n');
    try {
      await navigator.clipboard.writeText(text);
      message.success(t('copied'));
    } catch {
      message.error(t('somethingWentWrong'));
    }
  }

  return (
    <Drawer open={open} onClose={onClose} title={title} width={560} destroyOnHidden>
      <Space wrap style={{ marginBottom: 16 }}>
        <Tag color="success" data-testid="bulk-ok-count">{t('pages.clients.bulkResult.ok', { count: ok.length })}</Tag>
        <Tag color="warning" data-testid="bulk-skipped-count">{t('pages.clients.bulkResult.skipped', { count: skipped.length })}</Tag>
        <Tag color="error" data-testid="bulk-failed-count">{t('pages.clients.bulkResult.failed', { count: failed.length })}</Tag>
      </Space>
      {skipped.length + failed.length === 0 ? (
        <Empty description={t('pages.clients.bulkResult.allDone')} />
      ) : (
        <>
          {failed.length > 0 && (
            <>
              <Typography.Title level={5}>{t('pages.clients.bulkResult.failed', { count: failed.length })}</Typography.Title>
              <Table<BulkRow> size="small" rowKey={(r) => r.email + r.reason} columns={columns} dataSource={failed} pagination={failed.length > 20 ? { pageSize: 20 } : false} />
              <Space style={{ margin: '8px 0 16px' }}>
                <Button onClick={copyFailed}>{t('pages.clients.bulkResult.copyFailed')}</Button>
                {onRetry && (
                  <Button type="primary" onClick={() => onRetry(Array.from(new Set(failed.map((r) => r.email))))}>
                    {t('pages.clients.bulkResult.retryFailed')}
                  </Button>
                )}
              </Space>
            </>
          )}
          {skipped.length > 0 && (
            <>
              <Typography.Title level={5}>{t('pages.clients.bulkResult.skipped', { count: skipped.length })}</Typography.Title>
              <Table<BulkRow> size="small" rowKey={(r) => r.email + r.reason} columns={columns} dataSource={skipped} pagination={skipped.length > 20 ? { pageSize: 20 } : false} />
            </>
          )}
        </>
      )}
    </Drawer>
  );
}
