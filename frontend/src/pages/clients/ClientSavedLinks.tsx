import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import ConfigBlock from '@/components/clients/ConfigBlock';
import type { ClientRecord, useClients } from '@/hooks/useClients';
import { isPostQuantumLink } from '@/lib/xray/inbound-link';
import { HttpUtil } from '@/utils';

export interface ClientSavedLinksProps {
  active: boolean;
  client: ClientRecord | null;
  subSettings?: ReturnType<typeof useClients>['subSettings'];
}

export default function ClientSavedLinks({ active, client, subSettings }: ClientSavedLinksProps) {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: ['clients', 'savedLinks', client?.subId],
    enabled: active && !!client?.subId,
    queryFn: async () => {
      const result = await HttpUtil.get(`/panel/api/clients/subLinks/${encodeURIComponent(client!.subId!)}`, undefined, { silent: true });
      if (!result?.success) throw new Error(result?.msg || t('somethingWentWrong'));
      return Array.isArray(result.obj) ? result.obj.filter((value): value is string => typeof value === 'string') : [];
    },
  });
  if (!client) return <p className="p-sub">{t('pages.clients.design.saveForLinks', { defaultValue: 'Save the client to generate its connection links.' })}</p>;
  const subscriptions = subSettings?.enable && client.subId ? [
    { label: t('subscription.title'), base: subSettings.subURI },
    { label: `${t('subscription.title')} · JSON`, base: subSettings.subJsonEnable ? subSettings.subJsonURI : '' },
    { label: `${t('subscription.title')} · Clash`, base: subSettings.subClashEnable ? subSettings.subClashURI : '' },
  ].filter((item) => item.base) : [];
  return <div className="client-form-saved-links">
    <div className="p-kicker">{t('pages.clients.design.savedLinks', { defaultValue: 'Saved connection links' })}</div>
    <span className="p-sub">{t('pages.clients.design.linksAfterSave', { defaultValue: 'These links describe the saved client. Save changes to update them.' })}</span>
    {query.isFetching && <Spin size="small" />}
    {query.isError && <Alert type="error" showIcon title={t('somethingWentWrong')} action={<Button size="small" onClick={() => void query.refetch()}>{t('refresh')}</Button>} />}
    {(query.data ?? []).map((link, index) => <ConfigBlock key={link} label={link.split('://')[0]} text={link}
      fileName={`${client.email}-${index + 1}.txt`} qrRemark={client.email} showQr={!isPostQuantumLink(link)} defaultOpen={index === 0} />)}
    {subscriptions.map((item) => <ConfigBlock key={item.label} label={item.label} text={item.base + client.subId}
      fileName={`${client.email}-subscription.txt`} qrRemark={client.email} tagColor="geekblue" />)}
  </div>;
}
