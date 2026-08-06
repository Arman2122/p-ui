import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import { CoreViewSchema } from '@/generated/zod';
import type { CoreView } from '@/generated/zod';

const CoreViewsSchema = z.array(CoreViewSchema);

async function fetchCores(): Promise<CoreView[]> {
  /* Only the success toast is suppressed: a form that cannot tell a WireGuard
     client from a VMess one must say so rather than render the wrong fields. */
  const msg = await HttpUtil.get('/panel/api/cores', undefined, { silentSuccess: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch cores');
  const validated = parseMsg(msg, CoreViewsSchema, 'cores');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

/* What this build can serve. It changes only when the binary does, so it is
   fetched once and never refetched. */
export function useCoresQuery() {
  return useQuery({
    queryKey: keys.cores.list(),
    queryFn: fetchCores,
    staleTime: Infinity,
  });
}
