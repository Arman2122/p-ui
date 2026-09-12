import type { ApiPreparedRequest } from './request-transport';

export type ApiSnippetLanguage = 'curl' | 'JavaScript' | 'Python';
const quoteShell = (value: string) => `'${value.replaceAll("'", "'\"'\"'")}'`;
const quote = (value: string) => JSON.stringify(value);

export function apiRequestSnippet(language: ApiSnippetLanguage, request: ApiPreparedRequest): string {
  const headers = { ...request.headers };
  if (request.auth.mode === 'bearer') headers.Authorization = `Bearer ${request.auth.token?.trim() ?? ''}`;
  const multipart = request.mediaType === 'multipart/form-data';
  const fields = request.fields ?? [];
  if (language === 'curl') {
    const lines: string[] = [];
    if (request.auth.mode === 'session') lines.push('COOKIE_JAR=/path/to/session-cookies.txt');
    if (request.requiresCsrf) lines.push(`CSRF=$(curl --fail --silent --show-error -b "$COOKIE_JAR" -c "$COOKIE_JAR" ${quoteShell(request.csrfUrl)} | python3 -c 'import json,sys; r=json.load(sys.stdin); assert r.get("success") is True; print(r["obj"])')`);
    const parts = [`curl --request ${quoteShell(request.method)} ${quoteShell(request.url)}`];
    if (request.auth.mode === 'session') parts.push('-b "$COOKIE_JAR" -c "$COOKIE_JAR"');
    for (const [key, value] of Object.entries(headers)) parts.push(`--header ${quoteShell(`${key}: ${value}`)}`);
    if (request.requiresCsrf) parts.push('--header "X-CSRF-Token: $CSRF"');
    if (multipart) for (const [name, value] of fields) parts.push(value instanceof File ? `--form ${quoteShell(`${name}=@${value.name}`)}` : `--form-string ${quoteShell(`${name}=${value}`)}`);
    else if (typeof request.body === 'string') parts.push(`--data-raw ${quoteShell(request.body)}`);
    lines.push(parts.join(' \\\n  '));
    return lines.join('\n');
  }
  if (language === 'JavaScript') {
    const lines = [`const headers = ${JSON.stringify(headers, null, 2)};`];
    if (request.requiresCsrf) lines.push(`const csrfResponse = await fetch(${quote(request.csrfUrl)}, { credentials: 'same-origin', redirect: 'error' });`,
      `if (!csrfResponse.ok) throw new Error('CSRF: ' + csrfResponse.status);`,
      'const csrf = await csrfResponse.json();', `if (csrf.success !== true || !csrf.obj) throw new Error('CSRF unavailable');`, `headers['X-CSRF-Token'] = csrf.obj;`);
    if (multipart) {
      lines.push('const body = new FormData();');
      for (const [name, value] of fields) lines.push(value instanceof File
        ? `body.append(${quote(name)}, document.getElementById(${quote(`api-file-${encodeURIComponent(name)}`)}).files[0], ${quote(value.name)});`
        : `body.append(${quote(name)}, ${quote(value)});`);
    }
    lines.push(`const response = await fetch(${quote(request.url)}, {`, `  method: ${quote(request.method)},`, '  headers,',
      `  credentials: ${quote(request.auth.mode === 'session' ? 'same-origin' : 'omit')},`, `  redirect: 'error',`,
      ...(multipart ? ['  body,'] : typeof request.body === 'string' ? [`  body: ${quote(request.body)},`] : []), '});',
      'console.log(response.status, Object.fromEntries(response.headers));',
      `const contentType = response.headers.get('content-type') || '';`,
      `const responseBody = /json|text|xml/.test(contentType) ? await response.text() : await response.blob();`,
      'console.log(responseBody);');
    return lines.join('\n');
  }
  const lines = ['import json', 'import requests'];
  if (request.auth.mode === 'session') lines.push('from http.cookiejar import MozillaCookieJar');
  lines.push('', 'session = requests.Session()', 'session.trust_env = False');
  if (request.auth.mode === 'session') lines.push('cookies = MozillaCookieJar("/path/to/session-cookies.txt")', 'cookies.load(ignore_discard=True, ignore_expires=True)', 'session.cookies = cookies');
  lines.push(`headers = json.loads(${quote(JSON.stringify(headers))})`);
  if (request.requiresCsrf) lines.push(`csrf_response = session.get(${quote(request.csrfUrl)}, allow_redirects=False)`, 'csrf_response.raise_for_status()', 'csrf = csrf_response.json()', 'assert csrf.get("success") is True and csrf.get("obj")', 'headers["X-CSRF-Token"] = csrf["obj"]');
  if (multipart) {
    lines.push('files = [');
    for (const [name, value] of fields) lines.push(value instanceof File ? `    (${quote(name)}, (${quote(value.name)}, open(${quote(value.name)}, "rb"))),`
      : `    (${quote(name)}, (None, ${quote(value)})),`);
    lines.push(']');
  }
  lines.push(`response = session.request(${quote(request.method)}, ${quote(request.url)},`, '    headers=headers, allow_redirects=False,',
    ...(multipart ? ['    files=files,'] : typeof request.body === 'string' ? [`    data=${quote(request.body)}.encode("utf-8"),`] : []), ')',
    'print(response.status_code, dict(response.headers))', 'print(response.text if any(t in response.headers.get("content-type", "") for t in ("json", "text", "xml")) else response.content)');
  if (multipart) lines.push('for _, value in files:', '    if hasattr(value[1], "close"):', '        value[1].close()');
  return lines.join('\n');
}
