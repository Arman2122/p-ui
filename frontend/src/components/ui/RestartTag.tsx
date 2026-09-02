import { Tag, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';

/* Marks a setting the panel reads only at start, so the operator knows which
   saves need a restart and which apply at once. F402. */
export default function RestartTag() {
  const { t } = useTranslation();
  return (
    <Tooltip title={t('pages.settings.restartTagTip')}>
      <Tag color="warning" style={{ marginLeft: 8 }}>{t('pages.settings.restartTag')}</Tag>
    </Tooltip>
  );
}
