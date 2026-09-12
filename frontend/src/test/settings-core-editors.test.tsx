import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { FormProvider, useForm } from 'react-hook-form';
import { AdvancedSliceEditor } from '@/pages/inbounds/form/advanced-editors';

vi.mock('@/components/form', () => ({
  JsonEditor: ({ value, onChange }: { value: string; onChange: (next: string) => void }) => <textarea aria-label="JSON" value={value} onChange={(event) => onChange(event.target.value)} />,
}));

describe('core configuration drafts', () => {
  it('retains an invalid draft and refuses an absent settings envelope instead of clearing clients', () => {
    const onValidity = vi.fn();
    const onRead = vi.fn();
    function Harness() {
      const methods = useForm({ defaultValues: { settings: { clients: [{ email: 'kept@example.test' }] } } });
      return <FormProvider {...methods}>
        <AdvancedSliceEditor path="settings" wrapKey="settings" onValidityChange={onValidity} />
        <button onClick={() => onRead(methods.getValues('settings'))}>Read</button>
      </FormProvider>;
    }
    render(<Harness />);
    fireEvent.change(screen.getByLabelText('JSON'), { target: { value: '{' } });
    expect(onValidity).toHaveBeenLastCalledWith(false);
    expect((screen.getByLabelText('JSON') as HTMLTextAreaElement).value).toBe('{');
    fireEvent.change(screen.getByLabelText('JSON'), { target: { value: '{"different":{}}' } });
    expect(onValidity).toHaveBeenLastCalledWith(false);
    fireEvent.change(screen.getByLabelText('JSON'), { target: { value: '{"settings":null}' } });
    expect(onValidity).toHaveBeenLastCalledWith(false);
    fireEvent.click(screen.getByText('Read'));
    expect(onRead).toHaveBeenLastCalledWith({ clients: [{ email: 'kept@example.test' }] });
    fireEvent.change(screen.getByLabelText('JSON'), { target: { value: '{"settings":{"clients":[{"email":"new@example.test"}]}}' } });
    expect(onValidity).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByText('Read'));
    expect(onRead).toHaveBeenLastCalledWith({ clients: [{ email: 'new@example.test' }] });
  });
});
