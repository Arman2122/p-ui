import { createDefaultMtprotoInboundSettings } from '@/lib/xray/inbound-defaults';
import { MtprotoClientSchema } from '@/schemas/protocols/inbound';

import { TCP, type KindModule } from './module';

export const mtproto: KindModule = {
  defaultSettings: createDefaultMtprotoInboundSettings,
  clientSchema: MtprotoClientSchema,
  transports: () => TCP,
  multiUser: true,
  shareLink: true,
};
