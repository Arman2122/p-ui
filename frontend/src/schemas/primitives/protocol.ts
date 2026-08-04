import { PROTOCOL_VALUES, ProtocolSchema } from '@/generated/zod';

export { ProtocolSchema };
export type Protocol = (typeof PROTOCOL_VALUES)[number];

/*
Named constants over the generated list, so `Protocols.VLESS` still reads as
intended while the list itself stays generated from the Go constants.

The mapped type keeps each value its own literal — `Protocols.VLESS` is 'vless',
not the whole union — and makes the map exhaustive by construction, so a new
protocol needs no edit here at all. The assertion is what Object.fromEntries
costs: it erases key types and there is no overload that preserves them.
*/
export const Protocols = Object.freeze(
  Object.fromEntries(PROTOCOL_VALUES.map((protocol) => [protocol.toUpperCase(), protocol])),
) as { readonly [K in Protocol as Uppercase<K>]: K };
