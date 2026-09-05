import { useTranslation } from 'react-i18next';
import { Tag, Tooltip } from 'antd';

import { stateTone } from '@/lib/cores/status';

interface CoreStateTagProps {
  state: string;
  ready: boolean;
  /* Why it is not ready, or the daemon's last words; shown on hover. */
  detail?: string;
}

const COLOR_BY_TONE = { success: 'success', warning: 'warning', error: 'error', default: 'default' } as const;

/* One badge for state and readiness together: a daemon that runs and does not
   answer is not green, whatever its pid says. */
export function CoreStateTag({ state, ready, detail }: CoreStateTagProps) {
  const { t } = useTranslation();
  const { tone, labelKey } = stateTone(state, ready);
  const tag = <Tag color={COLOR_BY_TONE[tone]}>{t(labelKey)}</Tag>;
  return detail ? <Tooltip title={detail}>{tag}</Tooltip> : tag;
}
