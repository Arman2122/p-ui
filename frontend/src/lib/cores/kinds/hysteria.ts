import { createDefaultHysteriaInboundSettings } from '@/lib/xray/inbound-defaults';
import { HysteriaClientSchema } from '@/schemas/protocols/inbound';

import { UDP, type KindModule } from './module';

export const hysteria: KindModule = {
  defaultSettings: createDefaultHysteriaInboundSettings,
  clientSchema: HysteriaClientSchema,
  transports: () => UDP,
  multiUser: true,
  shareLink: true,
};
