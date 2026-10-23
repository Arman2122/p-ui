import { fireEvent, waitFor } from '@testing-library/react';
import { useState } from 'react';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { afterEach, describe, expect, it, vi } from 'vitest';

import DateTimePicker from '@/components/form/DateTimePicker';
import { setDatepicker } from '@/hooks/useDatepicker';
import { renderWithProviders } from './test-utils';

afterEach(() => setDatepicker('gregorian'));

function openPicker(): void {
  const input = document.querySelector('.ant-picker input');
  if (!input) throw new Error('picker input not rendered');
  fireEvent.mouseDown(input);
  fireEvent.click(input);
}

function clickDayCell(title: string): void {
  const cell = document.querySelector(`.ant-picker-cell[title="${title}"] .ant-picker-cell-inner`);
  if (!cell) throw new Error(`day cell ${title} not rendered`);
  fireEvent.click(cell);
}

describe('DateTimePicker', () => {
  it('keeps a cleared Jalali expiry empty after the picker remounts', async () => {
    setDatepicker('jalalian');
    function ControlledPicker() {
      const [value, setValue] = useState<Dayjs | null>(dayjs('2026-12-31'));
      return <><DateTimePicker value={value} onChange={setValue} /><output>{value ? value.valueOf() : 'empty'}</output></>;
    }
    const { container } = renderWithProviders(<ControlledPicker />);
    const clear = container.querySelector('.jdp-clear');
    if (!clear) throw new Error('Jalali clear button not rendered');
    fireEvent.click(clear);
    await waitFor(() => expect(container.querySelector('output')?.textContent).toBe('empty'));
    expect(container.querySelector('.jdp-empty')).not.toBeNull();
  });

  it('commits a clicked calendar date without an OK press', () => {
    const onChange = vi.fn<(next: Dayjs | null) => void>();
    renderWithProviders(<DateTimePicker value={null} onChange={onChange} />);

    openPicker();
    const tomorrow = dayjs().add(1, 'day').format('YYYY-MM-DD');
    clickDayCell(tomorrow);

    expect(onChange).toHaveBeenCalled();
    const committed = onChange.mock.calls.at(-1)?.[0];
    expect(committed?.format('YYYY-MM-DD HH:mm:ss')).toBe(`${tomorrow} 00:00:00`);
  });

  it('renders no OK confirm button in the picker footer', () => {
    renderWithProviders(<DateTimePicker value={null} onChange={vi.fn()} />);

    openPicker();

    expect(document.querySelector('.ant-picker-ok')).toBeNull();
  });
});
