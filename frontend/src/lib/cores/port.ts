import type { CoreView } from '@/generated/zod';

/*
Whether an inbound of this kind binds a port at all.

An Xray TUN device is a kernel interface, not a listener, so its port is a form
field and nothing else — the panel used to demand one, then count it as occupied
and refuse a real inbound there.

Asked of what each core declares (`portlessKinds`), never of a protocol list
here: a core added later declares for itself, and a list in the frontend is the
thing that goes stale. A kind no core claims is treated as binding a port, which
is what every kind did before this existed. F319.
*/
export function kindBindsNoPort(cores: CoreView[] | undefined, kind: string | undefined): boolean {
  if (!cores || !kind) return false;
  for (const core of cores) {
    if ((core.portlessKinds ?? []).includes(kind)) return true;
  }
  return false;
}
