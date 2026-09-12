import { fireEvent, render, screen } from '@testing-library/react';
import { FormProvider, useForm } from 'react-hook-form';
import { describe, expect, it, vi } from 'vitest';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';

import DateTimePicker from '@/components/form/DateTimePicker';
import { FormField } from '@/components/form/rhf';
import { setDatepicker } from '@/hooks/useDatepicker';
import { ThemeProvider } from '@/hooks/useTheme';

vi.mock('persian-calendar-suite', async () => {
  const browserBundle = 'persian-calendar-suite/dist/index.esm.js';
  return import(browserBundle);
});

function ExpiryField({ id, ariaLabel }: { id?: string; ariaLabel?: string }) {
  const form = useForm<{ expiry: Dayjs | null }>({ defaultValues: { expiry: dayjs('2026-12-31') } });
  return <FormProvider {...form}>
    <FormField name="expiry" label="Expiry">
      <DateTimePicker id={id} aria-label={ariaLabel} value={null} onChange={() => undefined} />
    </FormField>
  </FormProvider>;
}

describe.each(['gregorian', 'jalalian'] as const)('DateTimePicker labels in %s', (calendar) => {
  it('links the FormField label to its input and forwards explicit accessible names', () => {
    setDatepicker(calendar);
    const view = render(<ExpiryField />, { wrapper: ThemeProvider });
    const original = screen.getByLabelText('Expiry', { selector: 'input' });
    const label = screen.getByText('Expiry', { selector: 'label' }) as HTMLLabelElement;
    expect(label.control).toBe(original);
    expect(original.id).toBe(label.htmlFor);

    view.rerender(<ExpiryField id="review-expiry" ariaLabel="Expiry date and time" />);
    const named = screen.getByRole('textbox', { name: 'Expiry date and time' });
    expect(named.id).toBe('review-expiry');
    expect(screen.getByLabelText('Expiry', { selector: 'input' })).toBe(named);

    if (calendar === 'jalalian') {
      fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
      const cleared = screen.getByRole('textbox', { name: 'Expiry date and time' });
      expect(cleared).not.toBe(named);
      expect(cleared.id).toBe('review-expiry');
      expect(screen.getByLabelText('Expiry', { selector: 'input' })).toBe(cleared);
    }
  });
});
