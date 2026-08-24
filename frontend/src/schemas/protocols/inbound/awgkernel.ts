import { z } from 'zod';

import { optionalClearedInt } from './wireguard';

import { WgkernelInboundSettingsSchema } from './wgkernel';

/*
AmneziaWG's obfuscation parameters, as the panel stores them on the inbound.

Every one of these is part of a CLIENT's configuration rather than a
server-side knob: the panel writes them into each .conf it hands out, and a
client whose values differ cannot recognise the server's packets at all —
measured, a mismatched pair does not degrade, it never completes a handshake.

h1..h4 and the timers cross the API as range TEXT like "10-19", with a bare
number still accepted as the range [n, n] -- internal/awg/params_json.go owns
that encoding on both sides. They are listed here not because the form renders
them but because zod strips what it does not name, and the form saves the PARSED
object: leaving them out deleted any value set through the API the first time
anyone opened and saved that inbound.
*/
const rangeText = z.union([z.string(), z.number()]).optional();
export const AwgParamsSchema = z.object({
  jc: optionalClearedInt(z.number().int().min(0).max(65535)),
  jmin: optionalClearedInt(z.number().int().min(0).max(65535)),
  jmax: optionalClearedInt(z.number().int().min(0).max(65535)),
  s1: optionalClearedInt(z.number().int().min(0).max(65535)),
  s2: optionalClearedInt(z.number().int().min(0).max(65535)),
  s3: optionalClearedInt(z.number().int().min(0).max(65535)),
  s4: optionalClearedInt(z.number().int().min(0).max(65535)),
  h1: rangeText,
  h2: rangeText,
  h3: rangeText,
  h4: rangeText,
  i1: z.string().optional(),
  i2: z.string().optional(),
  i3: z.string().optional(),
  i4: z.string().optional(),
  i5: z.string().optional(),
  headerProtectionKey: z.string().optional(),
  randomTrailers: z.boolean().optional(),
  disableCookies: z.boolean().optional(),
  contentPaddingAddition: rangeText,
  rekeyAfterTime: rangeText,
  rekeyTimeout: rangeText,
  rejectAfterTime: rangeText,
  keepaliveTimeout: rangeText,
  maxHandshakeAttempts: rangeText,
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
