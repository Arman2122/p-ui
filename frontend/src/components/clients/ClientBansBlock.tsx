import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge, Button } from 'antd';

import { useBanMutations, useBansQuery } from '@/api/queries/useBansQuery';

/* One client's active bans beside their address log, each with a release: the
   address log is where an operator looks when a client says they cannot connect. */
export function ClientBansBlock({ email, open }: { email?: string; open: boolean }) {
  const { t } = useTranslation();
  const { bans } = useBansQuery(open && !!email);
  const { release, saving } = useBanMutations();
  const mine = useMemo(() => bans.filter((b) => b.clientEmail === email), [bans, email]);
  if (!email || mine.length === 0) return null;
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('pages.clients.activeBans')}</div>
      {mine.map((b) => (
        <div key={b.id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <Badge
            status={b.reason === 'shared-skipped' ? 'default' : 'error'}
            text={
              <span style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace' }}>
                {b.source}
                {b.node ? ` @ ${b.node}` : ''}
                {b.reason === 'shared-skipped' ? ` (${t('pages.settings.ipLimitBanReasonReported')})` : ''}
              </span>
            }
          />
          <Button type="link" size="small" disabled={saving} onClick={() => { void release(b.clientEmail, b.source); }}>
            {t('pages.clients.banRelease')}
          </Button>
        </div>
      ))}
    </div>
  );
}
