import type { z } from 'zod';

import type { AnyInboundSettings } from '@/lib/xray/inbound-defaults';

/*
What the panel knows about an inbound kind that no core declares over the API
yet: the defaults a fresh inbound starts from, the client record's canonical
projection, the transports the kind binds, whether it carries clients, whether
the panel's own link builder covers it, and what the inbound form shows for it.

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

/* Xray's selectable networks, sockopt alone, or Hysteria's own transport form. */
export type StreamMode = 'selectable' | 'none' | 'hysteria';

export interface KindModule {
  defaultSettings: () => AnyInboundSettings;
  /* null for a kind without a client list. */
  clientSchema: z.ZodType | null;
  /* What the core answers for its kind; absent leaves Xray's stream rules to decide. */
  transports?: (settings: Record<string, unknown> | undefined) => TransportBits;
  /* A function when the settings decide, as a Shadowsocks cipher does. */
  multiUser: boolean | ((settings: { method?: string }) => boolean);
  shareLink: boolean;
  /* Whether an inbound of the kind may live on a node; absent means local only. */
  nodeEligible?: boolean;
  /* Absent means selectable. Only read for a kind the stream capability allows. */
  stream?: StreamMode;
  /* Absent means the transport masks apply, as they do to every streamed kind but one. */
  transportMasks?: boolean;
  /* Absent means the kind has a settings editor of its own in the protocol tab. */
  settingsTab?: boolean;
  /* Mirrors the fallbacks capability's protocol term: the kind may host fallbacks on some transport. */
  fallbackHost?: boolean;
  /* The client form's reverse tag is a VLESS bridge's. */
  clientReverseTag?: boolean;
}
