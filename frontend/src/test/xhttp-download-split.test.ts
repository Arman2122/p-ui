import { describe, it, expect } from 'vitest';

import { parseVlessLink } from '@/lib/xray/outbound-link-parser';
import { XHttpStreamSettingsSchema } from '@/schemas/protocols/stream/xhttp';

/*
A split XHTTP link: upload to one address, download from another.

Two addresses on one server is the case this exists for — the upload leg is the
panel's own inbound, the download leg is a second address the same machine also
answers on. Only the upload half is a listener, so one inbound describes both
and the download half travels to clients inside the link's `extra` blob.

Built from an object rather than pasted pre-encoded, so the shape stays legible
and the encoding is the one a client would actually receive.
*/
const DOWNLOAD_SETTINGS = {
  address: 'download.example.com',
  port: 443,
  network: 'xhttp',
  security: 'tls',
  tlsSettings: {
    serverName: 'download.example.com',
    alpn: ['h2', 'http/1.1'],
    fingerprint: 'chrome',
  },
  xhttpSettings: { path: '/' },
};

const EXTRA = {
  mode: 'auto',
  scMaxEachPostBytes: '1000000',
  xPaddingBytes: '100-1000',
  downloadSettings: DOWNLOAD_SETTINGS,
};

const SPLIT_LINK =
  'vless://11111111-2222-3333-4444-555555555555@upload.example.com:443'
  + '?type=xhttp&security=tls&path=%2F&fp=chrome&alpn=h2,http%2F1.1&mode=auto'
  + `&extra=${encodeURIComponent(JSON.stringify(EXTRA))}#XHTTP-split`;

describe('split XHTTP: upload and download on different addresses', () => {
  it('keeps the download endpoint when a share link is imported', () => {
    const raw = parseVlessLink(SPLIT_LINK);
    expect(raw).not.toBeNull();

    const stream = raw?.streamSettings as Record<string, unknown>;
    expect(stream.network).toBe('xhttp');

    const xhttp = stream.xhttpSettings as Record<string, unknown>;
    const download = xhttp.downloadSettings as Record<string, unknown>;
    expect(download).toBeTruthy();
    expect(download.address).toBe('download.example.com');
    expect(download.port).toBe(443);
    expect(download.security).toBe('tls');
  });

  it('models the download endpoint instead of carrying it as an opaque blob', () => {
    const raw = parseVlessLink(SPLIT_LINK);
    const stream = raw?.streamSettings as Record<string, unknown>;
    const parsed = XHttpStreamSettingsSchema.parse(stream.xhttpSettings);

    /* The upload host stays on the URL authority; only the download half is
       described here, which is what lets one inbound express both. */
    expect(parsed.downloadSettings?.address).toBe('download.example.com');
    expect(parsed.downloadSettings?.port).toBe(443);
    expect(parsed.downloadSettings?.network).toBe('xhttp');
    expect(parsed.downloadSettings?.tlsSettings?.serverName).toBe('download.example.com');
    expect(parsed.downloadSettings?.tlsSettings?.alpn).toEqual(['h2', 'http/1.1']);
    expect(parsed.downloadSettings?.tlsSettings?.fingerprint).toBe('chrome');
    expect(parsed.downloadSettings?.xhttpSettings?.path).toBe('/');
  });

  it('refuses the one combination xray-core will not start with', () => {
    const streamOne = XHttpStreamSettingsSchema.safeParse({
      mode: 'stream-one',
      downloadSettings: { address: 'download.example.com', port: 443 },
    });
    expect(streamOne.success).toBe(false);
    expect(streamOne.error?.issues[0]?.message).toBe('xhttp.downloadSettingsNotInStreamOne');

    /* stream-up is the mode the split is actually used with, so the guard must
       not be a blanket ban on having a download endpoint at all. */
    const streamUp = XHttpStreamSettingsSchema.safeParse({
      mode: 'stream-up',
      downloadSettings: { address: 'download.example.com', port: 443 },
    });
    expect(streamUp.success).toBe(true);
  });
});
