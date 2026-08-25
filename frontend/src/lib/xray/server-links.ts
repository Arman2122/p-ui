import { HttpUtil } from '@/utils';

/*
Share links come from the server, because it is the only renderer that knows
about Host rows.

The browser's builder has no concept of a Host, so every panel surface that
rendered locally handed out the origin address on exactly the deployments hosts
exist for. It is kept for the live form preview, where there is no saved inbound
to ask about, and for kernel WireGuard, whose peers are not host-fronted.
*/
export type ServerLinks =
  | { status: 'ok'; links: string[] }
  | { status: 'error'; message: string };

/*
A failure must surface. Falling back to the local builder would put the origin
address back on screen at the exact moment the server is signalling a problem —
silently, and looking exactly like success.
*/
export async function fetchInboundLinks(inboundId: number, email?: string): Promise<ServerLinks> {
  const msg = await HttpUtil.get<string[]>(
    `/panel/api/inbounds/${inboundId}/links`,
    email ? { email } : undefined,
    { silentSuccess: true },
  );
  if (!msg?.success || !Array.isArray(msg.obj)) {
    return { status: 'error', message: msg?.msg || 'Failed to load links' };
  }
  return { status: 'ok', links: msg.obj };
}

// The server stamps each link's display name in its fragment, which is where
// the remark template has already been applied per client.
export function remarkFromLink(link: string): string {
  const hash = link.indexOf('#');
  if (hash < 0) return '';
  const raw = link.slice(hash + 1);
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}
