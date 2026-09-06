import { createDefaultAnytlsInboundSettings } from '@/lib/xray/inbound-defaults';
import { AnytlsClientSchema } from '@/schemas/protocols/inbound';

import { TCP, type KindModule } from './module';

export const anytls: KindModule = {
  defaultSettings: createDefaultAnytlsInboundSettings,
  clientSchema: AnytlsClientSchema,
  transports: () => TCP,
  multiUser: true,
  shareLink: false,
};
