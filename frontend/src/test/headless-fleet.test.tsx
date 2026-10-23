import { afterEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import HeadlessFleet from '@/pages/nodes/HeadlessFleet';
import { HttpUtil, Msg } from '@/utils';
import { makeTestQueryClient } from './test-utils';

const status = {
  ready: true,
  controlPlaneId: '6a2d2a2c-b977-4c21-a4a7-e6e7c4b126ab',
  controlAddress: 'control.example.com:7443',
  enrollmentUrl: 'https://control.example.com:7444/enroll',
  rootSha256: 'a'.repeat(64),
  certificateExpiresUnixMilli: 0,
  recoveryRequired: false,
  recoveryReason: '',
  error: '',
};

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function mount(recoveryRequired = false, nodes: unknown[] = []) {
  vi.mocked(HttpUtil.get).mockImplementation(async <T,>(url: string) => new Msg<T>(true, '', (
    url.endsWith('/status') ? { ...status, recoveryRequired } : { items: nodes, nextCursor: '' }
  ) as T));
  const client = makeTestQueryClient();
  return render(<QueryClientProvider client={client}><HeadlessFleet /></QueryClientProvider>);
}

it('blocks new enrollment while fleet recovery is required', async () => {
  mount(true);
  await screen.findByText('Fleet recovery is required');
  const button = screen.getByRole('button', { name: 'Enroll node' });
  expect((button as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(button);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(HttpUtil.post).not.toHaveBeenCalled();
});

it('shows deployment rejection separately from a connected node and stale runtime report', async () => {
	mount(false, [{
			id: status.controlPlaneId, name: 'test-node', enrolled: true, online: true, lastSeenUnixMilli: Date.now(),
			desiredRevision: 4, appliedRevision: 3, deploymentState: 'rejected', runtimeState: 'stale',
			runtimeObservedAt: Date.now() - 60000, runtimeConditions: [{ resourceId: 'xray', condition: 'CONFIGURATION_APPLIED', ready: true }],
		}]);
	await screen.findByText('Online');
	expect(screen.getByText('Deployment rejected')).toBeTruthy();
	expect(screen.getByText('Outdated report')).toBeTruthy();
});

it('downloads the enrollment package without rendering the private ticket', async () => {
  const secret = 'private-test-enrollment-ticket';
  const download = vi.fn();
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(download);
  const original = URL;
  class TestURL extends original {
    static override createObjectURL = vi.fn(() => 'blob:private-enrollment');
    static override revokeObjectURL = vi.fn();
  }
  vi.stubGlobal('URL', TestURL);
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', {
    nodeId: status.controlPlaneId, expiresUnixMilli: Date.now() + 600000,
    packageJson: JSON.stringify({ ticket: secret }),
  }));
  mount();
  const button = screen.getByRole('button', { name: 'Enroll node' });
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(button);
  fireEvent.change(screen.getByLabelText('Node name'), { target: { value: 'fra-1' } });
  fireEvent.click(screen.getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(download).toHaveBeenCalledOnce());
  expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/fleet/enroll', { name: 'fra-1' });
  expect(screen.queryByText(secret)).toBeNull();
  expect(TestURL.createObjectURL).toHaveBeenCalledOnce();
});
