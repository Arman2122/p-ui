import { z } from 'zod';

export const BackupScheduleSchema = z.object({
  enabled: z.boolean(),
  cadence: z.enum(['hourly', 'daily', 'weekly']),
  keep: z.number().int().min(1).max(365),
  encrypt: z.literal(false).default(false),
  sendToTelegram: z.boolean().default(false),
});
export const BackupCreateSchema = z.object({
  encrypt: z.boolean(), sendToTelegram: z.boolean(), passphrase: z.string().max(1024), confirmation: z.string().max(1024),
}).superRefine((value, ctx) => {
  if (value.encrypt && value.passphrase.length < 12) ctx.addIssue({ code: 'custom', path: ['passphrase'], message: 'settingsOps.backup.passphraseLength' });
  if (value.encrypt && value.passphrase !== value.confirmation) ctx.addIssue({ code: 'custom', path: ['confirmation'], message: 'settingsOps.backup.passphraseMismatch' });
});
export type BackupCreateValues = z.infer<typeof BackupCreateSchema>;
export const BackupRecordSchema = z.object({
  id: z.string(), operation: z.string(), source: z.string(), status: z.string(),
  startedAt: z.number(), finishedAt: z.number(), size: z.number(), filename: z.string(),
  error: z.string(), available: z.boolean(),
  encrypted: z.boolean().default(false), telegramStatus: z.string().default('not_requested'), telegramError: z.string().default(''),
});
export const BackupStatusSchema = z.object({
  schedule: BackupScheduleSchema, directory: z.string(), nextRun: z.number(),
  busy: z.boolean(), records: z.array(BackupRecordSchema),
  telegramAvailable: z.boolean(), managedCoreConfigs: z.boolean(), trafficHistory: z.boolean(),
});
export type BackupSchedule = z.infer<typeof BackupScheduleSchema>;
export type BackupRecord = z.infer<typeof BackupRecordSchema>;

export const CoreConfigInstanceSchema = z.object({
  id: z.number(), tag: z.string(), remark: z.string(), kind: z.string(), enabled: z.boolean(),
  listen: z.string(), port: z.number(), settings: z.string(), streamSettings: z.string(), sniffing: z.string(),
});
export const CoreConfigSchema = z.object({
  id: z.string(), titleKey: z.string(), template: z.string(), templateEditable: z.boolean(),
  instanceValidation: z.boolean(), instances: z.array(CoreConfigInstanceSchema),
});

export const SettingsAPITokenSchema = z.object({
  id: z.number().int(), name: z.string(), enabled: z.boolean(), createdAt: z.number(),
  scope: z.enum(['read', 'admin']), expiresAt: z.number(), allowedIPs: z.string().default(''),
});
export const SettingsAPITokenListSchema = z.array(SettingsAPITokenSchema);
export type SettingsAPIToken = z.infer<typeof SettingsAPITokenSchema>;
