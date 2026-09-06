import { createDefaultTuicInboundSettings } from '@/lib/xray/inbound-defaults';
import { TuicClientSchema } from '@/schemas/protocols/inbound';

import { UDP, type KindModule } from './module';

export const tuic: KindModule = {
  defaultSettings: createDefaultTuicInboundSettings,
  clientSchema: TuicClientSchema,
  transports: () => UDP,
  multiUser: true,
  shareLink: false,
};
