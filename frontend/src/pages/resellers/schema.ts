import { z } from 'zod';
import { ClientHistoryPointSchema, ClientPlanMetadataSchema } from '@/schemas/client';

export const PermissionsSchema = z.object({ plans: z.boolean(), reset: z.boolean(), extend: z.boolean(), suspend: z.boolean(), topUp: z.boolean() });
export const SignedInUserSchema = z.object({ id: z.number(), username: z.string(), role: z.enum(['admin', 'reseller']), home: z.string(), permissions: PermissionsSchema });
export type SignedInUser = z.infer<typeof SignedInUserSchema>;
export const OnDepletionSchema = z.enum(['disable', 'block']);
export const ResetCycleSchema = z.enum(['none', 'weekly', 'monthly']);
export const ResellerAccountSchema = z.object({
  userId: z.number(), name: z.string(), email: z.string(), trafficLimit: z.number(), trafficAllocated: z.number(), trafficUsed: z.number(), clientCap: z.number(), expiresAt: z.number(), suspended: z.boolean(), budgetBlocked: z.boolean(),
  allowPlans: z.boolean(), allowReset: z.boolean(), allowExtend: z.boolean(), allowSuspend: z.boolean(), allowTopUp: z.boolean(), notifyOnDepletion: z.boolean(), note: z.string().optional(), createdAt: z.number(), updatedAt: z.number(),
  onDepletion: OnDepletionSchema, resetCycle: ResetCycleSchema, periodStartedAt: z.number(),
});
export const ResellerViewSchema = z.object({ account: ResellerAccountSchema, username: z.string(), clients: z.number(), trafficUsed: z.number(), inboundIds: z.array(z.number()), nextResetAt: z.number(), online: z.number(), active: z.number(), history: z.array(ClientHistoryPointSchema).nullish() });
export type ResellerView = z.infer<typeof ResellerViewSchema>;
export const ResellerInboundSchema = z.object({ id: z.number(), remark: z.string(), protocol: z.string(), enable: z.boolean(), clients: z.number(), trafficUsed: z.number() });
export const ResellerClientSchema = z.object({ id: z.number(), email: z.string(), subId: z.string(), totalGB: z.number(), expiryTime: z.number(), limitIp: z.number(), enable: z.boolean(), comment: z.string(), trafficUsed: z.number(), lastOnline: z.number(), inboundIds: z.array(z.number()).nullable(), plan: ClientPlanMetadataSchema.nullish(), history: z.array(ClientHistoryPointSchema).nullish() });
export type ResellerClient = z.infer<typeof ResellerClientSchema>;
export const ResellerClientPageSchema = z.object({ items: z.array(ResellerClientSchema), total: z.number() });
export const ResellerDashboardSchema = z.object({ reseller: ResellerViewSchema, online: z.number(), active: z.number(), attention: z.array(ResellerClientSchema), inbounds: z.array(ResellerInboundSchema), sales: z.array(z.object({ day: z.string(), bytes: z.number() })) });
export const ResellerTokenSchema = z.object({ id: z.number(), name: z.string(), expiresAt: z.number(), createdAt: z.number(), token: z.string().optional() });
export const ResellerTransferResultSchema = z.object({ moved: z.number(), plansCleared: z.number() });
export const ClientPickerSchema = z.object({ items: z.array(z.object({ email: z.string() })) });

const nonnegative = z.number().min(0, 'resellers.invalid');
export const ResellerFormSchema = z.object({
  name: z.string().trim().min(1, 'resellers.required').max(128, 'resellers.invalid'), username: z.string().regex(/^[a-z0-9][a-z0-9_.-]{2,63}$/, 'resellers.invalidHandle'),
  email: z.union([z.literal(''), z.email('resellers.invalidEmail')]), password: z.string().refine((v) => v === '' || new TextEncoder().encode(v).length >= 12 && new TextEncoder().encode(v).length <= 72, 'resellers.passwordLength'),
  trafficGB: nonnegative, clientCap: nonnegative.int(), expiresAt: nonnegative, inboundIds: z.array(z.number()).min(1, 'resellers.grantRequired'),
  allowPlans: z.boolean(), allowReset: z.boolean(), allowExtend: z.boolean(), allowSuspend: z.boolean(), allowTopUp: z.boolean(), notifyOnDepletion: z.boolean(), note: z.string().max(2048, 'resellers.invalid'),
  onDepletion: OnDepletionSchema, resetCycle: ResetCycleSchema,
});
export type ResellerForm = z.infer<typeof ResellerFormSchema>;
export const ResellerClientFormSchema = z.object({ email: z.string().min(1, 'resellers.required').max(128, 'resellers.invalid'), trafficGB: nonnegative, expiryTime: nonnegative, limitIp: nonnegative.int(), enable: z.boolean(), comment: z.string().max(2048, 'resellers.invalid'), inboundIds: z.array(z.number()).min(1, 'resellers.grantRequired'), planId: nonnegative.int() });
export type ResellerClientForm = z.infer<typeof ResellerClientFormSchema>;
export const ResellerTopUpSchema = z.object({ trafficGB: nonnegative, days: nonnegative.int().max(36500, 'resellers.invalid'), clients: nonnegative.int() }).refine((v) => v.trafficGB + v.days + v.clients > 0, { message: 'resellers.required', path: ['trafficGB'] });
export const ResellerDeleteSchema = z.object({ clients: z.enum(['delete', 'transfer']), ownerId: z.number().int().min(0) });
export type ResellerDeleteForm = z.infer<typeof ResellerDeleteSchema>;
export const ResellerPasswordSchema = z.object({ current: z.string().min(1, 'resellers.required'), password: z.string().min(12, 'resellers.passwordLength').max(72, 'resellers.passwordLength') });
export const ResellerTokenFormSchema = z.object({ name: z.string().min(1, 'resellers.required').max(64, 'resellers.invalid'), days: z.number().int().min(1, 'resellers.invalid').max(365, 'resellers.invalid') });
