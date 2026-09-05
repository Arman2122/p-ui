import { useTranslation } from 'react-i18next';
import { Badge, Tooltip } from 'antd';

import { stateTone } from '@/lib/cores/status';

interface CoreStateBadgeProps {
  state: string;
  ready: boolean;
  /* Why it is not ready, or the daemon's last words; shown on hover. */
  detail?: string;
}

/* One badge for state and readiness together: a daemon that runs and does not
   answer is not green, whatever its pid says. A status dot beside plain text,
   not a coloured Tag: the preset tag palette fails WCAG contrast at 12px. */
export function CoreStateBadge({ state, ready, detail }: CoreStateBadgeProps) {
  const { t } = useTranslation();
  const { tone, labelKey } = stateTone(state, ready);
  const badge = <Badge status={tone} text={t(labelKey)} />;
  return detail ? <Tooltip title={detail}>{badge}</Tooltip> : badge;
}
