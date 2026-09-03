import { describe, expect, it, vi } from 'vitest';
import { Form } from 'antd';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { FormProvider, useForm } from 'react-hook-form';

import SshExitFields from '@/pages/xray/outbounds/exits/ssh-fields';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

function Harness() {
  const methods = useForm({ defaultValues: { sshHost: '91.107.151.123', sshPort: 12878, sshFingerprint: '' } });
  return (
    <FormProvider {...methods}>
      <Form>
        <SshExitFields />
      </Form>
    </FormProvider>
  );
}

/* The operator was refused three times for an empty fingerprint on a real
   panel; the button asks the panel to scan the key and fills the field. */
describe('SSH exit fingerprint fetch', () => {
  it('asks the panel for the host key and fills the field with it', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue({
      success: true, msg: '', warning: '', obj: { fingerprint: 'SHA256:slAhPUsjKT/+eP4DHWVimm+a5yTcX8Sw13dOOHWn/L4', keyType: 'ssh-ed25519' },
    });
    renderWithProviders(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Fetch from server' }));
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith('/panel/api/egresses/hostkey', { host: '91.107.151.123', port: 12878 }, expect.anything());
    });
    await waitFor(() => {
      expect((document.getElementById('sshFingerprint') as HTMLInputElement).value)
        .toBe('SHA256:slAhPUsjKT/+eP4DHWVimm+a5yTcX8Sw13dOOHWn/L4');
    });
  });
});
