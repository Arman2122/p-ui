import { describe, it, expect } from 'vitest';

import { parseVlessLink } from '@/lib/xray/outbound-link-parser';
import { normalizeXhttpForWire } from '@/lib/xray/stream-wire-normalize';
import { XHttpStreamSettingsSchema, xhttpSplitConflictsWithMode } from '@/schemas/protocols/stream/xhttp';

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

  it('still parses the one combination xray-core will not start with', () => {
    /* Parsing must stay tolerant: this schema also reads rows the panel did not
       write, and rejecting them would drop the form back to the raw object and
       lose every default. The combination is refused where it is acted on, not
       where it is read. */
    const streamOne = XHttpStreamSettingsSchema.safeParse({
      mode: 'stream-one',
      downloadSettings: { address: 'download.example.com', port: 443 },
    });
    expect(streamOne.success).toBe(true);
    expect(xhttpSplitConflictsWithMode(streamOne.data!)).toBe(true);

    /* stream-up is the mode the split is actually used with, so the check must
       not be a blanket ban on having a download endpoint at all. */
    const streamUp = XHttpStreamSettingsSchema.parse({
      mode: 'stream-up',
      downloadSettings: { address: 'download.example.com', port: 443 },
    });
    expect(xhttpSplitConflictsWithMode(streamUp)).toBe(false);
  });
});

describe('the download endpoint survives an edit', () => {
  it('is kept when the settings never went through the form', () => {
    /* An imported config or an API write has the endpoint but not the UI
       toggle. Reading the absent toggle as "off" would delete a working
       download half on the next save — the failure the xmux comment in
       the schema describes, one field over. */
    const wire = normalizeXhttpForWire(
      { mode: 'stream-up', downloadSettings: { address: 'download.example.com', port: 443 } },
      'inbound',
    );
    expect(wire.downloadSettings).toBeTruthy();
  });

  it('is cleared only when the operator actually turns it off', () => {
    const off = normalizeXhttpForWire(
      {
        mode: 'stream-up',
        enableDownloadSettings: false,
        downloadSettings: { address: 'download.example.com', port: 443 },
      },
      'inbound',
    );
    expect(off.downloadSettings).toBeUndefined();
    expect(off.enableDownloadSettings).toBeUndefined();
  });

  it('never reaches the wire alongside stream-one', () => {
    const streamOne = normalizeXhttpForWire(
      {
        mode: 'stream-one',
        enableDownloadSettings: true,
        downloadSettings: { address: 'download.example.com', port: 443 },
      },
      'inbound',
    );
    expect(streamOne.downloadSettings).toBeUndefined();
  });
});
