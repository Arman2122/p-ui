import { z } from 'zod';

export const ApiRequestAuthSchema = z.object({
  mode: z.enum(['session', 'bearer']),
  token: z.string().optional(),
  identityKey: z.string().optional(),
});
export type ApiRequestAuth = z.infer<typeof ApiRequestAuthSchema>;

export interface ApiPreparedRequest {
  method: string;
  url: string;
  csrfUrl: string;
  headers: Record<string, string>;
  body?: string | FormData;
  fields?: [string, string | File][];
  mediaType?: string;
  auth: ApiRequestAuth;
  requiresCsrf: boolean;
}

export interface ApiLiveResponse {
  status: number;
  statusText: string;
  ok: boolean;
  elapsedMs: number;
  size: number;
  headers: [string, string][];
  kind: 'json' | 'text' | 'binary';
  body: string;
  blob: Blob;
  truncated: boolean;
  declaredSize?: number;
  envelopeFailed: boolean;
}

export class ApiRequestError extends Error {
  constructor(public key: string, public detail = '') { super(key); }
}

export const safeApiMethod = (method: string) => ['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase());
export const apiControlCharacters = (value: string) => [...value].some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127);
export const managedApiHeader = (name: string) => /^(authorization|cookie|cookie2|host|origin|referer|content-length|content-type|x-csrf-token|connection|transfer-encoding|accept-encoding|proxy-.*|sec-.*)$/i.test(name);

export function panelBasePath(basePath?: string): string {
  const configured = window.PUI_BASE_PATH || document.querySelector('meta[name="base-path"]')?.getAttribute('content');
  const raw = (basePath ?? configured ?? '/') || '/';
  if (!raw.startsWith('/') || raw.startsWith('//') || /[\\?# ]/.test(raw) || apiControlCharacters(raw)) throw new ApiRequestError('apiRepair.request.invalidBase');
  const clean = raw.replace(/\/+$/, '');
  if (configured && clean !== configured.replace(/\/+$/, '')) throw new ApiRequestError('apiRepair.request.invalidBase');
  const resolved = new URL(`${clean}/`, window.location.origin);
  if (resolved.origin !== window.location.origin || resolved.pathname !== `${clean}/`) throw new ApiRequestError('apiRepair.request.invalidBase');
  return clean;
}

export function panelRequestUrl(path: string, basePath?: string): string {
  const base = panelBasePath(basePath);
  if (!path.startsWith('/') || path.startsWith('//') || /[\\?# {}]/.test(path) || apiControlCharacters(path) || /(^|\/)[:*][^/]+/.test(path)) throw new ApiRequestError('apiRepair.request.invalidPath');
  const expected = `${base}${path}`;
  const url = new URL(expected, window.location.origin);
  if (url.origin !== window.location.origin || url.pathname !== expected || !url.pathname.startsWith(`${base}/`)) throw new ApiRequestError('apiRepair.request.invalidPath');
  return url.href;
}

export function encodeApiPathValue(value: string): string {
  if (!value.trim() || value === '.' || value === '..' || /[/\\]/.test(value) || apiControlCharacters(value)) throw new ApiRequestError('apiRepair.request.invalidPath');
  return encodeURIComponent(value).replace(/[!'()*]/g, (char) => `%${char.charCodeAt(0).toString(16).toUpperCase()}`);
}

const RESPONSE_LIMIT = 8 * 1024 * 1024;

export async function sendApiRequest(request: ApiPreparedRequest, signal: AbortSignal): Promise<ApiLiveResponse> {
  const expected = new URL(request.url);
  const base = new URL(request.csrfUrl).pathname.replace(/\/csrf-token$/, '');
  if (expected.origin !== window.location.origin || !expected.pathname.startsWith(`${panelBasePath(base)}/`)) throw new ApiRequestError('apiRepair.request.invalidBase');
  const headers = new Headers(request.headers);
  headers.set('X-Requested-With', 'XMLHttpRequest');
  if (request.auth.mode === 'bearer') {
    const token = request.auth.token?.trim();
    if (!token || /[\r\n]/.test(token)) throw new ApiRequestError('apiRepair.request.tokenRequired');
    headers.set('Authorization', `Bearer ${token}`);
  }
  if (request.requiresCsrf) {
    if (new URL(request.csrfUrl).origin !== window.location.origin) throw new ApiRequestError('apiRepair.request.invalidBase');
    const csrfResponse = await fetch(request.csrfUrl, { credentials: 'same-origin', redirect: 'error', cache: 'no-store', signal, headers: { 'X-Requested-With': 'XMLHttpRequest' } });
    if (!csrfResponse.ok) throw new ApiRequestError('apiRepair.request.csrfFailed', String(csrfResponse.status));
    let csrf: unknown;
    try { csrf = await csrfResponse.json(); } catch { throw new ApiRequestError('apiRepair.request.csrfFailed'); }
    if (!csrf || typeof csrf !== 'object' || !('success' in csrf) || csrf.success !== true || !('obj' in csrf) || typeof csrf.obj !== 'string' || !csrf.obj) throw new ApiRequestError('apiRepair.request.csrfFailed');
    headers.set('X-CSRF-Token', csrf.obj);
  }
  signal.throwIfAborted();
  const start = performance.now();
  const response = await fetch(request.url, {
    method: request.method, headers, body: request.body, signal, redirect: 'error', cache: 'no-store',
    credentials: request.auth.mode === 'session' ? 'same-origin' : 'omit',
  });
  const chunks: Uint8Array<ArrayBuffer>[] = [];
  let size = 0, truncated = false;
  const reader = response.body?.getReader();
  if (reader) {
    try {
      while (true) {
        signal.throwIfAborted();
        const { done, value } = await reader.read();
        if (done) break;
        const remaining = RESPONSE_LIMIT - size;
        const kept = value.slice(0, remaining);
        chunks.push(kept);
        size += kept.byteLength;
        if (kept.byteLength < value.byteLength) { truncated = true; await reader.cancel(); break; }
      }
    } finally { reader.releaseLock(); }
  }
  signal.throwIfAborted();
  const contentType = response.headers.get('content-type') || '';
  const blob = new Blob(chunks, { type: contentType || 'application/octet-stream' });
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  let kind: ApiLiveResponse['kind'] = /json/i.test(contentType) ? 'json'
    : /^(text\/|application\/(xml|javascript|x-www-form-urlencoded))/i.test(contentType) || !contentType ? 'text' : 'binary';
  const text = kind === 'binary' ? '' : new TextDecoder().decode(bytes);
  let envelopeFailed = false;
  if (kind !== 'binary') {
    try {
      const parsed: unknown = JSON.parse(text);
      kind = 'json';
      envelopeFailed = !!parsed && typeof parsed === 'object' && 'success' in parsed && parsed.success === false;
    } catch { kind = 'text'; }
  }
  const length = response.headers.get('content-length');
  return {
    status: response.status, statusText: response.statusText, ok: response.ok, elapsedMs: Math.round(performance.now() - start), size,
    headers: [...response.headers.entries()], kind, body: text, blob, truncated,
    declaredSize: length && /^\d+$/.test(length) ? Number(length) : undefined, envelopeFailed,
  };
}
