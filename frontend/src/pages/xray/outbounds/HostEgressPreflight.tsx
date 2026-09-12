import { Alert, Button } from 'antd';
import { useTranslation } from 'react-i18next';
import { useEgressPreflightQuery } from '@/api/queries/useEgressesQuery';

export default function HostEgressPreflight() {
  const { t } = useTranslation();
  const query = useEgressPreflightQuery();
  if (query.isError) return <Alert type="warning" showIcon className="outbound-egress-preflight"
    title={t('uiRepair.outbounds.metadataUnavailable')}
    action={<Button size="small" onClick={() => void query.refetch()}>{t('refresh')}</Button>} />;
  const report = query.data;
  if (!report || (report.ok && !report.notes.length)) return null;
  return <Alert type={report.ok ? 'warning' : 'error'} showIcon className="outbound-egress-preflight"
    styles={{ root: { padding: '8px 12px', margin: '8px 12px' }, title: { fontSize: 12 } }}
    title={<div>
      <span>{t(report.ok ? 'pages.xray.egress.preflightNotes' : 'pages.xray.egress.preflightBlocked')}</span>
      <div dir="ltr" style={{ textAlign: 'left', fontSize: 11 }}>
        {[...(report.ok ? [] : report.refusals), ...report.notes].map((line) => <div key={line}><code>{line}</code></div>)}
      </div>
    </div>} />;
}
