import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

/*
"2 minutes ago" in the reader's locale. Shared by the notification bell and the
dashboard activity card so one event never reads two different ways.
*/
export function useRelativeTime(): (at: number) => string {
  const { i18n } = useTranslation();
  return useMemo(() => {
    const rtf = new Intl.RelativeTimeFormat(i18n.language || 'en', { numeric: 'auto' });
    return (at: number) => {
      const seconds = Math.round((at - Date.now()) / 1000);
      const abs = Math.abs(seconds);
      if (abs < 60) return rtf.format(Math.trunc(seconds), 'second');
      if (abs < 3600) return rtf.format(Math.trunc(seconds / 60), 'minute');
      if (abs < 86400) return rtf.format(Math.trunc(seconds / 3600), 'hour');
      return rtf.format(Math.trunc(seconds / 86400), 'day');
    };
  }, [i18n.language]);
}
