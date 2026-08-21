import { useEffect, useState } from 'react';

import { HttpUtil } from '@/utils';
import type { ClientRecord, InboundOption } from '@/hooks/useClients';
import { isWireguardLike } from '@/lib/xray/kernel-wireguard';

export function isWireguardClient(client: ClientRecord | null | undefined): boolean {
  if (!client) return false;
  return !!(client.privateKey || client.publicKey || client.allowedIPs || client.preSharedKey || client.keepAlive);
}

export function findWireguardInbound(
  client: ClientRecord | null | undefined,
  inboundsById: Record<number, InboundOption>,
): InboundOption | undefined {
  return (client?.inboundIds || [])
    .map((id) => inboundsById[id])
    .find((ib) => isWireguardLike(ib?.protocol));
}

/*
The .conf comes from the server, rendered by the core that owns the inbound.

It used to be assembled here out of the handful of WireGuard fields
InboundOption carries, which is why an AmneziaWG client downloaded from this
page described a plain WireGuard tunnel: none of the obfuscation reached it,
and a tunnel where one side obfuscates and the other does not simply never
completes a handshake, silently, on both ends. The subscription already served
the right bytes; this asks for the same ones.
*/
export function useWireguardClientConfig(
  open: boolean,
  client: ClientRecord | null | undefined,
  inbound: InboundOption | undefined,
): string {
  const [text, setText] = useState('');
  const subId = client?.subId;
  const inboundId = inbound?.id;
  const eligible = open && !!subId && !!inboundId && isWireguardClient(client);

  useEffect(() => {
    if (!eligible) {
      setText('');
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const msg = await HttpUtil.get(
          `/panel/api/clients/configFile/${encodeURIComponent(String(subId))}/${inboundId}`,
        ) as { success?: boolean; obj?: { body?: string } };
        if (!cancelled) setText(msg?.success ? (msg.obj?.body ?? '') : '');
      } catch (_) {
        if (!cancelled) setText('');
      }
    })();
    return () => { cancelled = true; };
  }, [eligible, subId, inboundId]);

  return text;
}
