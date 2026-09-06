import type { z } from 'zod';

import type { AnyInboundSettings } from '@/lib/xray/inbound-defaults';

/*
What the panel knows about an inbound kind that no core declares over the API
yet: the defaults a fresh inbound starts from, the client record's canonical
projection, the transports the kind binds, whether it carries clients, and
whether the panel's own link builder covers it.

One module per kind, one line per kind in the registry; the compiler refuses a
kind the registry does not list. The descriptor renderer (design §9) is what
replaces the hand-written half of this with what each core declares.
*/

/* L4 transports a kind binds, as the tag suffix encodes them. */
export type TransportBits = number;
export const TCP: TransportBits = 1;
export const UDP: TransportBits = 2;
/* Only a kind that binds nothing answers zero bits. */
export const NONE: TransportBits = 0;

export interface KindModule {
  defaultSettings: () => AnyInboundSettings;
  /* null for a kind without a client list. */
  clientSchema: z.ZodType | null;
  /* What the core answers for its kind; absent leaves Xray's stream rules to decide. */
  transports?: (settings: Record<string, unknown> | undefined) => TransportBits;
  /* A function when the settings decide, as a Shadowsocks cipher does. */
  multiUser: boolean | ((settings: { method?: string }) => boolean);
  shareLink: boolean;
}
