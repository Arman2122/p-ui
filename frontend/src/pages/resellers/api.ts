import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import { HttpUtil } from '@/utils';
import { ResellerClientPageSchema, SignedInUserSchema, type ResellerClient } from './schema';
import { setDatepicker } from '@/hooks/useDatepicker';
import i18next from 'i18next';

export const GiB = 1024 ** 3;
export const resellerKeys = ['resellers'] as const;

export async function resellerGet<T>(path: string, schema: z.ZodType<T>, params?: Record<string, string | number>): Promise<T> {
  const msg = await HttpUtil.get(`/panel/api${path}`, params, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'resellers.loadError');
  return schema.parse(msg.obj);
}

export async function resellerPost(path: string, body: unknown = {}): Promise<unknown> {
  const msg = await HttpUtil.post(`/panel/api${path}`, body, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'resellers.saveError');
  return msg.obj;
}

export function useSignedInUser() {
  return useQuery({ queryKey: ['signed-in-user'], queryFn: async () => {
    const user = await resellerGet('/me', SignedInUserSchema);
    window.PUI_ROLE = user.role;
    window.PUI_USER = user.username;
    if (user.role === 'reseller') setDatepicker(i18next.language.startsWith('fa') ? 'jalalian' : 'gregorian');
    return user;
  }, staleTime: 30_000, retry: false });
}

export async function exportOwnedClients(): Promise<void> {
  const all: ResellerClient[] = [];
  let page = 1;
  for (;;) {
    const result = await resellerGet('/reseller/clients', ResellerClientPageSchema, { page: page++, size: 200 });
    all.push(...result.items);
    if (all.length >= result.total || result.items.length === 0) break;
  }
  const url = URL.createObjectURL(new Blob([JSON.stringify(all, null, 2)], { type: 'application/json' }));
  const a = document.createElement('a'); a.href = url; a.download = 'reseller-clients.json'; a.click(); URL.revokeObjectURL(url);
}
