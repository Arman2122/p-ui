import { describe, expect, it } from 'vitest';

import { genMtprotoWebLink, mtprotoDDSecret } from '@/lib/xray/inbound-link';
import { parseLinkParts } from '@/lib/xray/link-label';

const ee = 'ee8196fe6ed8b637d001f91d6952cfcdf07777772e636c6f7564666c6172652e636f6d';

describe('the WEB proxy link', () => {
  it('spells the same key as dd, from hex or base64, and nothing else', () => {
    expect(mtprotoDDSecret(ee)).toBe('dd8196fe6ed8b637d001f91d6952cfcdf0');
    expect(mtprotoDDSecret('7oGW_m7YtjfQAfkdaVLPzfB3d3cuY2xvdWRmbGFyZS5jb20')).toBe('dd8196fe6ed8b637d001f91d6952cfcdf0');
    expect(mtprotoDDSecret('dd8196fe6ed8b637d001f91d6952cfcdf0')).toBe('');
    expect(mtprotoDDSecret('ee8196')).toBe('');
    expect(mtprotoDDSecret('')).toBe('');
  });

  it('carries the hostname and the dd secret, and no port', () => {
    const link = genMtprotoWebLink({ host: 'web.example.com', clientSecret: ee });
    const url = new URL(link);
    expect(url.protocol).toBe('tg:');
    expect(url.host).toBe('webproxy');
    expect(url.searchParams.get('server')).toBe('web.example.com');
    expect(url.searchParams.get('secret')).toBe('dd8196fe6ed8b637d001f91d6952cfcdf0');
    expect(url.searchParams.has('port')).toBe(false);
    expect(genMtprotoWebLink({ host: '', clientSecret: ee })).toBe('');
    expect(genMtprotoWebLink({ host: 'web.example.com', clientSecret: 'dd00' })).toBe('');
  });

  it('is labelled WEB where a FakeTLS link is labelled FakeTLS', () => {
    expect(parseLinkParts(genMtprotoWebLink({ host: 'web.example.com', clientSecret: ee }))?.security).toBe('WEB');
    expect(parseLinkParts('tg://proxy?server=1.2.3.4&port=443&secret=' + ee)?.security).toBe('FAKETLS');
  });
});
