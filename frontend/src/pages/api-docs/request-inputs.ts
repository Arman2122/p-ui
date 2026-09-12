import { getOperation, getParameters, getRequest, resolveLocalRef, type OpenApiDocument, type ApiMedia, type ApiParameter } from './api-document';
import type { Endpoint } from './endpoints';
import { ApiRequestError, apiControlCharacters, encodeApiPathValue, managedApiHeader, panelRequestUrl, safeApiMethod, type ApiPreparedRequest, type ApiRequestAuth } from './request-transport';

export interface ApiInputIssue { field: string; key: string }
export interface ApiRequestDraft { values: Record<string, string>; json: string; files: Record<string, File[]>; mediaType: string }
export const apiValueKey = (location: string, name: string) => `${location}:${name}`;
export const schemaObject = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};

export function requestMedia(document: OpenApiDocument, endpoint: Endpoint): ApiMedia[] {
  const operation = getOperation(document, endpoint);
  return operation ? getRequest(document, operation)?.content ?? [] : [];
}

export function requestFields(media?: ApiMedia): ApiParameter[] {
  const root = schemaObject(media?.schema);
  const required = Array.isArray(root.required) ? root.required : [];
  return Object.entries(schemaObject(root.properties)).map(([name, schema]) => {
    const field = schemaObject(schema);
    return { name, in: 'query' as const, required: required.includes(name), description: typeof field.description === 'string' ? field.description : '',
      type: field.format === 'binary' ? 'file' : typeof field.type === 'string' ? field.type : 'string', schema, examples: [], deprecated: false };
  });
}

export function initialRequestDraft(document: OpenApiDocument, endpoint: Endpoint): ApiRequestDraft {
  const media = requestMedia(document, endpoint);
  const values: Record<string, string> = {};
  for (const param of getParameters(document, endpoint)) {
    const initial = schemaObject(param.schema).default;
    if (initial !== undefined && !managedApiHeader(param.name)) values[apiValueKey(param.in, param.name)] = typeof initial === 'object' ? JSON.stringify(initial) : String(initial);
  }
  return { values, json: '', files: {}, mediaType: media[0]?.mediaType ?? '' };
}

function typeMatches(type: unknown, value: unknown): boolean {
  if (Array.isArray(type)) return type.some((item) => typeMatches(item, value));
  if (type === 'null') return value === null;
  if (type === 'object') return !!value && typeof value === 'object' && !Array.isArray(value);
  if (type === 'array') return Array.isArray(value);
  if (type === 'integer') return typeof value === 'number' && Number.isSafeInteger(value);
  if (type === 'number') return typeof value === 'number' && Number.isFinite(value);
  return !type || typeof value === type;
}

export function validateApiValue(document: OpenApiDocument, schema: unknown, value: unknown, field = '$', depth = 0): ApiInputIssue[] {
  if (schema === false) return [{ field, key: 'apiRepair.request.invalidValue' }];
  if (depth > 32) return [{ field, key: 'apiRepair.request.schemaLimit' }];
  let rules = schemaObject(schema);
  if (rules.$ref) {
    const resolved = resolveLocalRef(document, rules);
    rules = schemaObject(resolved.value);
    if (rules.$ref) return [{ field, key: 'apiRepair.request.schemaUnsupported' }];
  }
  if (value === null && rules.nullable === true) return [];
  const issues: ApiInputIssue[] = [];
  const fail = (key = 'apiRepair.request.invalidValue') => issues.push({ field, key });
  if (!typeMatches(rules.type, value)) { fail(); return issues; }
  if (Array.isArray(rules.enum) && !rules.enum.some((item) => JSON.stringify(item) === JSON.stringify(value))) fail();
  if ('const' in rules && JSON.stringify(rules.const) !== JSON.stringify(value)) fail();
  if (Array.isArray(rules.allOf)) for (const variant of rules.allOf) issues.push(...validateApiValue(document, variant, value, field, depth + 1));
  for (const composition of ['anyOf', 'oneOf']) {
    const variants = rules[composition];
    if (Array.isArray(variants)) {
      const matches = variants.filter((variant) => !validateApiValue(document, variant, value, field, depth + 1).length).length;
      if (!matches || (composition === 'oneOf' && matches !== 1)) fail();
    }
  }
  if (typeof value === 'number') {
    if (typeof rules.minimum === 'number' && (value < rules.minimum || (rules.exclusiveMinimum === true && value === rules.minimum))) fail();
    if (typeof rules.maximum === 'number' && (value > rules.maximum || (rules.exclusiveMaximum === true && value === rules.maximum))) fail();
    if (typeof rules.exclusiveMinimum === 'number' && value <= rules.exclusiveMinimum) fail();
    if (typeof rules.exclusiveMaximum === 'number' && value >= rules.exclusiveMaximum) fail();
  }
  if (typeof value === 'string') {
    if (typeof rules.minLength === 'number' && value.length < rules.minLength) fail();
    if (typeof rules.maxLength === 'number' && value.length > rules.maxLength) fail();
    if (typeof rules.pattern === 'string') {
      try { if (!new RegExp(rules.pattern).test(value)) fail(); } catch { fail('apiRepair.request.schemaUnsupported'); }
    }
    if (rules.format === 'date-time' && (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(value) || !Number.isFinite(Date.parse(value)))) fail();
  }
  if (Array.isArray(value)) {
    if (typeof rules.minItems === 'number' && value.length < rules.minItems) fail();
    if (typeof rules.maxItems === 'number' && value.length > rules.maxItems) fail();
    value.forEach((item, index) => issues.push(...validateApiValue(document, rules.items, item, `${field}[${index}]`, depth + 1)));
  } else if (value && typeof value === 'object') {
    const object = schemaObject(value), properties = schemaObject(rules.properties);
    for (const name of Array.isArray(rules.required) ? rules.required : []) {
      if (typeof name === 'string' && !Object.hasOwn(object, name)) issues.push({ field: `${field}.${name}`, key: 'apiRepair.request.required' });
    }
    for (const [name, item] of Object.entries(object)) {
      if (Object.hasOwn(properties, name)) issues.push(...validateApiValue(document, properties[name], item, `${field}.${name}`, depth + 1));
      else if (rules.additionalProperties === false) issues.push({ field: `${field}.${name}`, key: 'apiRepair.request.unknownField' });
      else if (rules.additionalProperties && typeof rules.additionalProperties === 'object') issues.push(...validateApiValue(document, rules.additionalProperties, item, `${field}.${name}`, depth + 1));
    }
  }
  return issues;
}

function parseInput(param: ApiParameter, raw: string): unknown {
  const type = schemaObject(param.schema).type ?? param.type;
  if (type === 'string' || type === 'file') return raw;
  if (type === 'integer' || type === 'number') {
    if (!/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(raw.trim())) throw new Error('number');
    return Number(raw);
  }
  if (type === 'boolean') {
    if (raw !== 'true' && raw !== 'false') throw new Error('boolean');
    return raw === 'true';
  }
  return JSON.parse(raw);
}

function parameterPairs(param: ApiParameter, value: unknown): [string, string][] {
  const style = param.style ?? 'form', explode = param.explode ?? style === 'form';
  const scalar = (item: unknown) => item && typeof item === 'object' ? JSON.stringify(item) : String(item);
  if (!['form', 'spaceDelimited', 'pipeDelimited', 'deepObject'].includes(style) || param.allowReserved) throw new ApiRequestError('apiRepair.request.serializationUnsupported');
  if (Array.isArray(value)) {
    if (style === 'form' && explode) return value.map((item) => [param.name, scalar(item)]);
    if (style === 'deepObject') throw new ApiRequestError('apiRepair.request.serializationUnsupported');
    return [[param.name, value.map(scalar).join(style === 'spaceDelimited' ? ' ' : style === 'pipeDelimited' ? '|' : ',')]];
  }
  if (value && typeof value === 'object') {
    const entries = Object.entries(value);
    if (style === 'deepObject') return entries.map(([name, item]) => [`${param.name}[${name}]`, scalar(item)]);
    if (style !== 'form') throw new ApiRequestError('apiRepair.request.serializationUnsupported');
    return explode ? entries.map(([name, item]) => [name, scalar(item)]) : [[param.name, entries.flatMap(([name, item]) => [name, scalar(item)]).join(',')]];
  }
  return [[param.name, scalar(value)]];
}

function simpleParameter(param: ApiParameter, value: unknown, path: boolean): string {
  if (param.style && param.style !== 'simple') throw new ApiRequestError('apiRepair.request.serializationUnsupported');
  const encode = (item: unknown) => path ? encodeApiPathValue(String(item)) : String(item);
  if (Array.isArray(value)) return value.map(encode).join(',');
  if (value && typeof value === 'object') return Object.entries(value).map(([name, item]) => param.explode ? `${encode(name)}=${encode(item)}` : `${encode(name)},${encode(item)}`).join(',');
  return encode(value);
}

export function prepareApiRequest(document: OpenApiDocument, endpoint: Endpoint, draft: ApiRequestDraft, auth: ApiRequestAuth, basePath?: string): { request?: ApiPreparedRequest; issues: ApiInputIssue[] } {
  const issues: ApiInputIssue[] = [];
  const fail = (field: string, key: string) => { issues.push({ field, key }); };
  const operation = getOperation(document, endpoint);
  if (!operation || endpoint.method === 'WS' || endpoint.path === '/ws' || /\{(?:sub|json|clash)Path\}/.test(endpoint.path)) return { issues: [{ field: endpoint.path, key: 'apiRepair.request.unavailable' }] };
  const item = schemaObject(resolveLocalRef(document, document.paths[endpoint.path.replace(/:([A-Za-z0-9_]+)/g, '{$1}')], { shallow: true }).value);
  const inputRefs = resolveLocalRef(document, { parameters: operation.parameters, inherited: item.parameters, requestBody: operation.requestBody }, { maxDepth: 64, maxNodes: 20000 });
  if (inputRefs.issues.length) fail('schema', 'apiRepair.request.schemaUnsupported');
  if (auth.mode === 'bearer' && (!auth.token?.trim() || /[\r\n]/.test(auth.token))) fail('Authorization', 'apiRepair.request.tokenRequired');
  if (auth.mode === 'bearer' && !endpoint.path.startsWith('/panel/api/')) fail('Authorization', 'apiRepair.request.sessionOnly');
  let path = endpoint.path.replace(/:([A-Za-z0-9_]+)/g, '{$1}');
  const query = new URLSearchParams(), headers: Record<string, string> = {};
  for (const param of getParameters(document, endpoint)) {
    const key = apiValueKey(param.in, param.name), raw = draft.values[key] ?? '';
    if (param.in === 'header' && managedApiHeader(param.name)) continue;
    if (param.in === 'cookie') { if (param.required) fail(param.name, 'apiRepair.request.cookieManaged'); continue; }
    if (!raw.trim()) { if (param.required || param.in === 'path') fail(param.name, 'apiRepair.request.required'); continue; }
    try {
      const value = parseInput(param, raw);
      issues.push(...validateApiValue(document, param.schema, value, param.name));
      if (param.in === 'path') path = path.replaceAll(`{${param.name}}`, simpleParameter(param, value, true));
      if (param.in === 'query') {
        for (const [name, item] of parameterPairs(param, value)) query.append(name, item);
      }
      if (param.in === 'header') {
        const text = simpleParameter(param, value, false);
        if (apiControlCharacters(text)) throw new Error('header');
        new Headers([[param.name, text]]);
        headers[param.name] = text;
      }
    } catch (error) { fail(param.name, error instanceof ApiRequestError ? error.key : 'apiRepair.request.invalidValue'); }
  }
  const bodySpec = getRequest(document, operation);
  const media = bodySpec?.content.find((item) => item.mediaType === draft.mediaType);
  let body: string | FormData | undefined, fields: [string, string | File][] | undefined;
  if (bodySpec && !media) fail('body', 'apiRepair.request.unsupportedBody');
  if (media) {
    if (media.issues?.some((issue) => issue.path.startsWith('$/schema'))) fail('body', 'apiRepair.request.schemaUnsupported');
    if (/json/i.test(media.mediaType)) {
      if (!draft.json.trim()) { if (bodySpec?.required) fail('body', 'apiRepair.request.required'); }
      else {
        try { const value: unknown = JSON.parse(draft.json); issues.push(...validateApiValue(document, media.schema, value)); body = draft.json; }
        catch { fail('body', 'apiRepair.request.invalidJson'); }
      }
    } else if (['application/x-www-form-urlencoded', 'multipart/form-data'].includes(media.mediaType)) {
      fields = [];
      for (const param of requestFields(media)) {
        const key = apiValueKey('body', param.name), raw = draft.values[key] ?? '';
        if (param.type === 'file') {
          const selected = draft.files[param.name] ?? [];
          if (param.required && !selected.length) fail(param.name, 'apiRepair.request.required');
          for (const file of selected) fields.push([param.name, file]);
          continue;
        }
        if (!raw.trim()) { if (param.required) fail(param.name, 'apiRepair.request.required'); continue; }
        try {
          const value = parseInput(param, raw);
          issues.push(...validateApiValue(document, param.schema, value, param.name));
          const encoding = schemaObject(media.encoding?.[param.name]);
          const style = typeof encoding.style === 'string' ? encoding.style : undefined;
          const explode = typeof encoding.explode === 'boolean' ? encoding.explode : undefined;
          if (media.mediaType === 'application/x-www-form-urlencoded') fields.push(...parameterPairs({ ...param, style, explode }, value));
          else for (const item of Array.isArray(value) ? value : [value]) fields.push([param.name, typeof item === 'object' ? JSON.stringify(item) : String(item)]);
        } catch { fail(param.name, 'apiRepair.request.invalidValue'); }
      }
      if (media.mediaType === 'multipart/form-data') {
        body = new FormData();
        for (const [name, value] of fields) body.append(name, value);
      } else body = new URLSearchParams(fields as [string, string][]).toString();
    } else fail('body', 'apiRepair.request.unsupportedBody');
    if (body !== undefined && media.mediaType !== 'multipart/form-data') headers['Content-Type'] = media.mediaType;
    if (safeApiMethod(endpoint.method) && body !== undefined) fail('body', 'apiRepair.request.safeBody');
  }
  let url = '', csrfUrl = '';
  try { url = panelRequestUrl(path, basePath); csrfUrl = panelRequestUrl('/csrf-token', basePath); }
  catch (error) { fail('URL', error instanceof ApiRequestError ? error.key : 'apiRepair.request.invalidPath'); }
  if (issues.length) return { issues };
  if (query.size) url += `?${query}`;
  return { issues, request: { method: endpoint.method, url, csrfUrl, headers, body, fields, mediaType: media?.mediaType, auth,
    requiresCsrf: auth.mode === 'session' && !safeApiMethod(endpoint.method) } };
}

export function apiDestructive(endpoint: Endpoint): boolean {
  return endpoint.method === 'DELETE' || (!safeApiMethod(endpoint.method) && /\/(?:del(?:ete)?[^/]*|reset[^/]*|restore|clear[^/]*|purge|restart[^/]*|stop[^/]*|importDB)(?:\/|$)/i.test(endpoint.path));
}
