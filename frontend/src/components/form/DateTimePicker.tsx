import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CloseCircleFilled } from '@ant-design/icons';
import { DatePicker } from 'antd';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { PersianDateTimePicker } from 'persian-calendar-suite';

import { useDatepicker } from '@/hooks/useDatepicker';
import { useTheme } from '@/hooks/useTheme';
import './DateTimePicker.css';

interface DateTimePickerProps {
  id?: string;
  'aria-label'?: string;
  value: Dayjs | null;
  onChange: (next: Dayjs | null) => void;
  showTime?: boolean;
  format?: string;
  placeholder?: string;
  disabled?: boolean;
}

const LIGHT_THEME = {
  primaryColor: '#1677ff',
  backgroundColor: '#ffffff',
  borderColor: '#d9d9d9',
  hoverColor: 'rgba(22, 119, 255, 0.10)',
  selectedTextColor: '#ffffff',
  textColor: 'rgba(0, 0, 0, 0.88)',
};

const DARK_THEME = {
  primaryColor: '#1677ff',
  backgroundColor: '#23252b',
  borderColor: 'rgba(255, 255, 255, 0.12)',
  hoverColor: 'rgba(22, 119, 255, 0.18)',
  selectedTextColor: '#ffffff',
  textColor: 'rgba(255, 255, 255, 0.88)',
};

const ULTRA_DARK_THEME = {
  primaryColor: '#1677ff',
  backgroundColor: '#101013',
  borderColor: 'rgba(255, 255, 255, 0.08)',
  hoverColor: 'rgba(22, 119, 255, 0.16)',
  selectedTextColor: '#ffffff',
  textColor: 'rgba(255, 255, 255, 0.88)',
};

export default function DateTimePicker({
  id,
  'aria-label': ariaLabel,
  value,
  onChange,
  showTime = true,
  format = 'YYYY-MM-DD HH:mm:ss',
  placeholder = '',
  disabled = false,
}: DateTimePickerProps) {
  const { t } = useTranslation();
  const { datepicker } = useDatepicker();
  const { isDark, isUltra } = useTheme();
  const jalaliRef = useRef<HTMLDivElement>(null);
  // Bumped on clear: persian-calendar-suite reads `value` only on mount, so
  // remounting via key is the only way to reflect an externally cleared value.
  const [clearNonce, setClearNonce] = useState(0);

  const persianTheme = useMemo(() => {
    if (isUltra) return ULTRA_DARK_THEME;
    if (isDark) return DARK_THEME;
    return LIGHT_THEME;
  }, [isDark, isUltra]);

  /* The library does not forward input props, including after a clear remount. */
  useEffect(() => {
    if (datepicker !== 'jalalian') return;
    const input = jalaliRef.current?.querySelector('input');
    if (!input) return;
    input.placeholder = placeholder;
    if (id !== undefined) input.id = id;
    else input.removeAttribute('id');
    if (ariaLabel !== undefined) input.setAttribute('aria-label', ariaLabel);
    else input.removeAttribute('aria-label');
  });

  if (datepicker === 'jalalian') {
    return (
      <div ref={jalaliRef} className={`jdp-wrap${isDark ? ' jdp-dark' : ''}${isUltra ? ' jdp-ultra' : ''}${disabled ? ' jdp-disabled' : ''}`}>
        <PersianDateTimePicker
          key={clearNonce}
          value={value ? value.valueOf() : null}
          onChange={(next: number | string | null) => {
            if (next == null || next === '') {
              onChange(null);
              return;
            }
            const ms = typeof next === 'number' ? next : Number(next);
            if (Number.isFinite(ms)) onChange(dayjs(ms));
          }}
          showTime={showTime}
          outputFormat="timestamp"
          persianNumbers
          rtlCalendar
          theme={persianTheme}
        />
        {value && !disabled && (
          <button
            type="button"
            className="jdp-clear"
            aria-label={t('clear')}
            onMouseDown={(e) => e.preventDefault()}
            onClick={(e) => {
              e.stopPropagation();
              onChange(null);
              setClearNonce((n) => n + 1);
            }}
          >
            <CloseCircleFilled />
          </button>
        )}
      </div>
    );
  }

  return (
    <DatePicker
      id={id}
      aria-label={ariaLabel}
      value={value}
      onChange={(next) => onChange(next || null)}
      onCalendarChange={(next) => onChange((Array.isArray(next) ? next[0] : next) || null)}
      showTime={showTime ? { format: 'HH:mm:ss' } : false}
      needConfirm={false}
      format={format}
      placeholder={placeholder}
      disabled={disabled}
      style={{ width: '100%' }}
    />
  );
}
