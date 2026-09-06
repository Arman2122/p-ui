import { createDefaultOcservInboundSettings } from '@/lib/xray/inbound-defaults';
import { OcservClientSchema } from '@/schemas/protocols/inbound';

import { TCP, UDP, type KindModule } from './module';

export const ocserv: KindModule = {
  defaultSettings: createDefaultOcservInboundSettings,
  clientSchema: OcservClientSchema,
  /* One port carries both: TLS for the session, DTLS for the data channel. */
  transports: () => TCP | UDP,
  multiUser: true,
  shareLink: false,
};
