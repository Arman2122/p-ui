import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { BackupCreateSchema, BackupScheduleSchema } from '@/schemas/settings-operations';
import BackupSection from '@/pages/settings/BackupSection';
import { renderWithProviders } from './test-utils';
import i18next from 'i18next';

const calls = vi.hoisted(() => ({ post: vi.fn() }));
vi.mock('@/utils', async (original) => ({ ...await original<typeof import('@/utils')>(), HttpUtil: {
  get: async () => ({ success: true, obj: { schedule: { enabled: false, cadence: 'daily', keep: 14, encrypt: false, sendToTelegram: false }, directory: '/test/backups', nextRun: 0, busy: false, records: [], telegramAvailable: true, managedCoreConfigs: true, trafficHistory: true } }),
  post: calls.post,
} }));

describe('backup encryption controls', () => {
  it('requires a matching passphrase and refuses unattended encryption', () => {
    expect(BackupCreateSchema.safeParse({ encrypt: true, passphrase: 'twelve random words', confirmation: 'different words', sendToTelegram: false }).success).toBe(false);
    expect(BackupCreateSchema.safeParse({ encrypt: true, passphrase: '', confirmation: '', sendToTelegram: false }).success).toBe(false);
    expect(BackupScheduleSchema.safeParse({ enabled: true, cadence: 'daily', keep: 14, encrypt: true, sendToTelegram: false }).success).toBe(false);
  });

  it('sends the passphrase only with archive creation and clears its input immediately', async () => {
    calls.post.mockResolvedValue({ success: true, obj: {} });
    renderWithProviders(<BackupSection />);
    const passphrase = await screen.findByLabelText(String(i18next.t('settingsOps.backup.passphrase')));
    const confirmation = screen.getByLabelText(String(i18next.t('settingsOps.backup.confirmPassphrase')));
    fireEvent.change(passphrase, { target: { value: 'unique words stored offline' } });
    fireEvent.change(confirmation, { target: { value: 'unique words stored offline' } });
    fireEvent.click(screen.getByRole('button', { name: String(i18next.t('settingsOps.backup.now')) }));
    await waitFor(() => expect(calls.post).toHaveBeenCalledWith('/panel/api/backups/create', { encrypt: true, passphrase: 'unique words stored offline', sendToTelegram: false }, { timeout: 1_900_000 }));
    expect((passphrase as HTMLInputElement).value).toBe('');
    expect((confirmation as HTMLInputElement).value).toBe('');
    expect(calls.post.mock.calls.every(([path]) => path !== '/panel/api/backups/schedule')).toBe(true);
  });
});
