import { z } from 'zod';

import { optionalClearedInt } from './wireguard';

import { WgkernelInboundSettingsSchema } from './wgkernel';

/*
AmneziaWG's obfuscation parameters, as the panel stores them on the inbound.

Every one of these is part of a CLIENT's configuration rather than a
server-side knob: the panel writes them into each .conf it hands out, and a
client whose values differ cannot recognise the server's packets at all —
measured, a mismatched pair does not degrade, it never completes a handshake.

h1..h4 are absent on purpose. They are u64 range encodings, and accepting a
number here would store something the kernel reads as a different range; they
stay settable through the API until that encoding has a home.
*/
export const AwgParamsSchema = z.object({
  jc: optionalClearedInt(z.number().int().min(0).max(65535)),
  jmin: optionalClearedInt(z.number().int().min(0).max(65535)),
  jmax: optionalClearedInt(z.number().int().min(0).max(65535)),
  s1: optionalClearedInt(z.number().int().min(0).max(65535)),
  s2: optionalClearedInt(z.number().int().min(0).max(65535)),
  s3: optionalClearedInt(z.number().int().min(0).max(65535)),
  s4: optionalClearedInt(z.number().int().min(0).max(65535)),
  i1: z.string().optional(),
  i2: z.string().optional(),
  i3: z.string().optional(),
  i4: z.string().optional(),
  i5: z.string().optional(),
  headerProtectionKey: z.string().optional(),
  randomTrailers: z.boolean().optional(),
  disableCookies: z.boolean().optional(),
});
export type AwgParams = z.infer<typeof AwgParamsSchema>;

/*
AmneziaWG (`awgkernel`) is kernel WireGuard plus obfuscation, so the device half
is that schema exactly — same keypair, address, MTU and DNS, same clients — and
only `awg` is added. Forking it would mean two schemas drifting apart over a
device that is genuinely the same one.
*/
export const AwgkernelInboundSettingsSchema = WgkernelInboundSettingsSchema.extend({
  awg: AwgParamsSchema.optional(),
});
export type AwgkernelInboundSettings = z.infer<typeof AwgkernelInboundSettingsSchema>;
