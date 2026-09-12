import { z } from 'zod';
import { sections, type Endpoint, type HttpMethod, type Section } from './endpoints';

/* OpenAPI extensions and JSON Schema keywords vary across generators; validate
   known structure while preserving additional vocabulary as unknown. */
const ReferenceSchema = z.object({ $ref: z.string() }).catchall(z.unknown());
const SchemaObject = z.object({
  $ref: z.string().optional(), type: z.union([z.string(), z.array(z.string())]).optional(),
  title: z.string().optional(), description: z.string().optional(), format: z.string().optional(),
  nullable: z.boolean().optional(), required: z.array(z.string()).optional(),
  properties: z.record(z.string(), z.unknown()).optional(), items: z.unknown().optional(),
  allOf: z.array(z.unknown()).optional(), oneOf: z.array(z.unknown()).optional(), anyOf: z.array(z.unknown()).optional(),
}).catchall(z.unknown());
export const OpenApiSchemaSchema = z.union([z.boolean(), SchemaObject]);
const ExampleObject = z.object({ summary: z.string().optional(), description: z.string().optional(), value: z.unknown().optional(), externalValue: z.string().optional() }).catchall(z.unknown());
const MediaSchema = z.object({ schema: OpenApiSchemaSchema.optional(), example: z.unknown().optional(), examples: z.record(z.string(), z.union([ReferenceSchema, ExampleObject])).optional(), encoding: z.record(z.string(), z.unknown()).optional() }).catchall(z.unknown());
const ContentSchema = z.record(z.string(), MediaSchema);
const ParameterObject = z.object({
  name: z.string(), in: z.enum(['path', 'query', 'header', 'cookie']), required: z.boolean().optional(),
  description: z.string().optional(), deprecated: z.boolean().optional(), schema: OpenApiSchemaSchema.optional(),
  style: z.string().optional(), explode: z.boolean().optional(), allowReserved: z.boolean().optional(),
  example: z.unknown().optional(), examples: z.record(z.string(), z.union([ReferenceSchema, ExampleObject])).optional(), content: ContentSchema.optional(),
}).catchall(z.unknown());
const ParameterSchema = z.union([ReferenceSchema, ParameterObject]);
const HeaderSchema = ParameterObject.omit({ name: true, in: true });
const RequestBodyObject = z.object({ required: z.boolean().optional(), description: z.string().optional(), content: ContentSchema }).catchall(z.unknown());
const ResponseObject = z.object({ description: z.string().optional(), headers: z.record(z.string(), z.union([ReferenceSchema, HeaderSchema])).optional(), content: ContentSchema.optional() }).catchall(z.unknown());
const SecuritySchema = z.array(z.record(z.string(), z.array(z.string())));
const ServerSchema = z.object({ url: z.string(), description: z.string().optional(), variables: z.record(z.string(), z.object({ default: z.string(), enum: z.array(z.string()).optional(), description: z.string().optional() }).catchall(z.unknown())).optional() }).catchall(z.unknown());
export const OpenApiOperationSchema = z.object({
  operationId: z.string().optional(), summary: z.string().optional(), description: z.string().optional(),
  tags: z.array(z.string()).optional(), deprecated: z.boolean().optional(), parameters: z.array(ParameterSchema).optional(),
  requestBody: z.union([ReferenceSchema, RequestBodyObject]).optional(),
  responses: z.record(z.string(), z.union([ReferenceSchema, ResponseObject])).optional(),
  security: SecuritySchema.optional(), servers: z.array(ServerSchema).optional(),
}).catchall(z.unknown());
const PathItemSchema = z.object({
  $ref: z.string().optional(), summary: z.string().optional(), description: z.string().optional(),
  parameters: z.array(ParameterSchema).optional(), servers: z.array(ServerSchema).optional(),
  get: OpenApiOperationSchema.optional(), post: OpenApiOperationSchema.optional(), put: OpenApiOperationSchema.optional(),
  patch: OpenApiOperationSchema.optional(), delete: OpenApiOperationSchema.optional(),
  head: OpenApiOperationSchema.optional(), options: OpenApiOperationSchema.optional(), trace: OpenApiOperationSchema.optional(),
}).catchall(z.unknown());
const WebSocketMessageSchema = z.object({ type: z.string(), summary: z.string().optional(), example: z.unknown().optional() }).catchall(z.unknown());
export const OpenApiDocumentSchema = z.object({
  openapi: z.string().regex(/^3\.\d+\.\d+$/), info: z.object({ title: z.string(), version: z.string(), description: z.string().optional() }).catchall(z.unknown()),
  paths: z.record(z.string(), PathItemSchema), tags: z.array(z.object({ name: z.string(), description: z.string().optional() }).catchall(z.unknown())).optional(),
  servers: z.array(ServerSchema).optional(), security: SecuritySchema.optional(),
  components: z.object({
    schemas: z.record(z.string(), OpenApiSchemaSchema).optional(),
    parameters: z.record(z.string(), ParameterSchema).optional(),
    requestBodies: z.record(z.string(), z.union([ReferenceSchema, RequestBodyObject])).optional(),
    responses: z.record(z.string(), z.union([ReferenceSchema, ResponseObject])).optional(),
    examples: z.record(z.string(), z.union([ReferenceSchema, ExampleObject])).optional(),
    headers: z.record(z.string(), z.union([ReferenceSchema, HeaderSchema])).optional(),
    securitySchemes: z.record(z.string(), z.record(z.string(), z.unknown())).optional(),
  }).catchall(z.unknown()).optional(),
  'x-websocket-messages': z.array(WebSocketMessageSchema).optional(),
}).catchall(z.unknown());
export const ApiDocumentSchema = OpenApiDocumentSchema;
export type OpenApiDocument = z.infer<typeof OpenApiDocumentSchema>;
export type ApiDocument = OpenApiDocument;
export type OpenApiOperation = z.infer<typeof OpenApiOperationSchema>;

export const ApiExampleSchema = z.object({ name: z.string(), value: z.unknown().optional(), summary: z.string().optional(), externalValue: z.string().optional() });
export const RefIssueSchema = z.object({ code: z.enum(['external', 'missing', 'cycle', 'limit', 'invalid']), path: z.string(), ref: z.string().optional() });
export const ApiSchemaFieldSchema = z.object({ name: z.string(), path: z.string(), type: z.string(), required: z.boolean(), description: z.string(), nullable: z.boolean(), depth: z.number(), schema: z.unknown().optional() });
export const ApiParameterSchema = z.object({ name: z.string(), in: z.enum(['path', 'query', 'header', 'cookie']), required: z.boolean(), description: z.string(), type: z.string(), schema: z.unknown().optional(), examples: z.array(ApiExampleSchema), deprecated: z.boolean(), style: z.string().optional(), explode: z.boolean().optional(), allowReserved: z.boolean().optional() });
export const ApiMediaSchema = z.object({ mediaType: z.string(), schema: z.unknown().optional(), fields: z.array(ApiSchemaFieldSchema), examples: z.array(ApiExampleSchema), isBinary: z.boolean(), encoding: z.record(z.string(), z.unknown()).optional(), issues: z.array(RefIssueSchema).optional() });
export const ApiRequestBodySchema = z.object({ required: z.boolean(), description: z.string(), content: z.array(ApiMediaSchema) });
export const ApiResponseSchema = z.object({ status: z.string(), description: z.string(), headers: z.array(ApiParameterSchema), content: z.array(ApiMediaSchema) });
export const ApiEntrySchema = z.object({
  key: z.string(), method: z.enum(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'WS']), path: z.string(), specPath: z.string(),
  groupId: z.string(), group: z.string(), groupDescription: z.string(), summary: z.string(), description: z.string(), deprecated: z.boolean(),
  kind: z.enum(['http', 'websocket']), available: z.boolean(), messageType: z.string().optional(), examples: z.array(ApiExampleSchema),
  subHeader: z.array(z.object({ name: z.string(), desc: z.string() })).optional(),
});
export const ApiOperationDetailsSchema = z.object({
  kind: z.enum(['http', 'websocket']), operationId: z.string().optional(), parameters: z.array(ApiParameterSchema),
  requestBody: ApiRequestBodySchema.optional(), responses: z.array(ApiResponseSchema), examples: z.array(ApiExampleSchema),
  security: SecuritySchema, servers: z.array(ServerSchema), issues: z.array(RefIssueSchema),
});
export type ApiExample = z.infer<typeof ApiExampleSchema>;
export type RefIssue = z.infer<typeof RefIssueSchema>;
export type ApiSchemaField = z.infer<typeof ApiSchemaFieldSchema>;
export type ApiParameter = z.infer<typeof ApiParameterSchema>;
export type ApiMedia = z.infer<typeof ApiMediaSchema>;
export type ApiRequestBody = z.infer<typeof ApiRequestBodySchema>;
export type ApiResponse = z.infer<typeof ApiResponseSchema>;
export type ApiEntry = z.infer<typeof ApiEntrySchema>;
export type ApiOperationDetails = z.infer<typeof ApiOperationDetailsSchema>;

const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] as const;
const forbiddenKeys = new Set(['__proto__', 'prototype', 'constructor']);
const annotations = new Set(['example', 'examples', 'default', 'enum', 'const']);
const namedMaps = new Set(['properties', 'patternProperties', 'definitions', '$defs', 'schemas', 'responses', 'headers', 'content', 'requestBodies', 'parameters']);
const object = (value: unknown): Record<string, unknown> | undefined => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
const bounded = (value: number | undefined, fallback: number, maximum: number) => value !== undefined && Number.isFinite(value) ? Math.min(maximum, Math.max(1, Math.floor(value))) : fallback;

/* References never fetch URLs or traverse prototypes; unresolved edges remain
   visible as $ref objects, with bounded diagnostics instead of recursion. */
export function resolveLocalRef(document: OpenApiDocument, value: unknown, limits: { maxDepth?: number; maxNodes?: number; shallow?: boolean } = {}): { value: unknown; issues: RefIssue[] } {
  const maxDepth = bounded(limits.maxDepth, 32, 64);
  const maxNodes = bounded(limits.maxNodes, 4000, 20000);
  const issues: RefIssue[] = [];
  let nodes = 0;
  const issue = (code: RefIssue['code'], path: string, ref?: string) => {
    if (issues.length < 100) issues.push({ code, path, ...(ref === undefined ? {} : { ref }) });
  };
  const visit = (node: unknown, depth: number, path: string, refs: Set<string>, ancestors: Set<object>, namedMap = false): unknown => {
    if (++nodes > maxNodes || depth > maxDepth) { issue('limit', path); return object(node)?.$ref ? { $ref: object(node)?.$ref } : undefined; }
    if (node === null || typeof node !== 'object') return node;
    if (limits.shallow && typeof object(node)?.$ref !== 'string') return node;
    if (ancestors.has(node)) { issue('cycle', path); return undefined; }
    const nextAncestors = new Set(ancestors).add(node);
    if (Array.isArray(node)) {
      const result: unknown[] = [];
      for (let i = 0; i < node.length && nodes <= maxNodes; i++) result.push(visit(node[i], depth + 1, `${path}/${i}`, refs, nextAncestors));
      return result;
    }
    const record = object(node)!;
    if (typeof record.$ref === 'string') {
      const ref = record.$ref;
      if (!ref.startsWith('#')) { issue('external', path, ref); return { $ref: ref }; }
      if (refs.has(ref)) { issue('cycle', path, ref); return { $ref: ref }; }
      let pointer: string;
      try { pointer = decodeURIComponent(ref.slice(1)); } catch { issue('invalid', path, ref); return { $ref: ref }; }
      if (pointer !== '' && !pointer.startsWith('/')) { issue('invalid', path, ref); return { $ref: ref }; }
      let target: unknown = document;
      for (const encoded of pointer === '' ? [] : pointer.slice(1).split('/')) {
        if (/~(?:[^01]|$)/.test(encoded)) { issue('invalid', path, ref); return { $ref: ref }; }
        const key = encoded.replace(/~1/g, '/').replace(/~0/g, '~');
        if (forbiddenKeys.has(key) || target === null || typeof target !== 'object' || !Object.hasOwn(target, key)) {
          issue('missing', path, ref); return { $ref: ref };
        }
        target = (target as Record<string, unknown>)[key];
      }
      const resolved = visit(target, depth + 1, path, new Set(refs).add(ref), nextAncestors);
      const siblings = Object.fromEntries(Object.entries(record).filter(([key]) => key !== '$ref' && !forbiddenKeys.has(key)));
      if (!Object.keys(siblings).length || !object(resolved)) return resolved;
      return { ...object(resolved), ...object(visit(siblings, depth + 1, path, refs, nextAncestors)) };
    }
    const entries: [string, unknown][] = [];
    for (const [key, child] of Object.entries(record)) {
      if (nodes > maxNodes) break;
      if (forbiddenKeys.has(key)) continue;
      entries.push([key, !namedMap && annotations.has(key) ? child : visit(child, depth + 1, `${path}/${key}`, refs, nextAncestors, !namedMap && namedMaps.has(key))]);
    }
    return Object.fromEntries(entries);
  };
  return { value: visit(value, 0, '$', new Set(), new Set()), issues };
}

export function normalizeApiPath(path: string): string { return path.replace(/:([A-Za-z_][A-Za-z0-9_]*)/g, '{$1}'); }

function pathItem(document: OpenApiDocument, path: string) {
  const raw = document.paths[normalizeApiPath(path)];
  if (!raw) return undefined;
  return PathItemSchema.safeParse(raw.$ref ? resolveLocalRef(document, raw, { shallow: true }).value : raw).data;
}

export function getOperation(document: OpenApiDocument, endpoint: Pick<Endpoint, 'method' | 'path'>): OpenApiOperation | undefined {
  if (endpoint.method === 'WS') return undefined;
  const raw = pathItem(document, endpoint.path)?.[endpoint.method.toLowerCase()];
  return OpenApiOperationSchema.safeParse(raw).data;
}

function schemaType(schema: unknown): string {
  if (schema === true) return 'any';
  if (schema === false) return 'never';
  const shape = object(schema);
  if (!shape) return 'any';
  if (shape.format === 'binary') return 'binary';
  if (Array.isArray(shape.type)) return shape.type.filter((type): type is string => typeof type === 'string').join(' | ');
  if (typeof shape.type === 'string') return shape.type;
  if (shape.properties) return 'object';
  if (shape.items) return 'array';
  if (shape.oneOf) return 'oneOf';
  if (shape.anyOf) return 'anyOf';
  if (shape.allOf) return 'allOf';
  if (typeof shape.$ref === 'string') return shape.$ref.split('/').at(-1) || '$ref';
  return 'any';
}

function schemaFieldResult(document: OpenApiDocument, schema: unknown): { fields: ApiSchemaField[]; truncated: boolean } {
  if (schema === undefined) return { fields: [], truncated: false };
  const resolved = resolveLocalRef(document, schema).value;
  const fields: ApiSchemaField[] = [];
  let truncated = false;
  const walk = (node: unknown, name: string, path: string, required: boolean, depth: number) => {
    if (fields.length >= 300 || depth > 32) { truncated = true; return; }
    const shape = object(node);
    fields.push({ name, path, required, depth, type: schemaType(node), description: typeof shape?.description === 'string' ? shape.description : '', nullable: shape?.nullable === true || (Array.isArray(shape?.type) && shape.type.includes('null')), schema: node });
    const requiredNames = Array.isArray(shape?.required) ? shape.required : [];
    for (const [key, child] of Object.entries(object(shape?.properties) ?? {})) walk(child, key, `${path}.${key}`, requiredNames.includes(key), depth + 1);
    if (shape?.items !== undefined) walk(shape.items, '[]', `${path}[]`, false, depth + 1);
    if (object(shape?.additionalProperties)) walk(shape?.additionalProperties, '*', `${path}.*`, false, depth + 1);
    for (const keyword of ['allOf', 'oneOf', 'anyOf'] as const) {
      const variants = shape?.[keyword];
      if (Array.isArray(variants)) variants.forEach((child, index) => walk(child, `${keyword}[${index}]`, `${path}.${keyword}[${index}]`, required, depth + 1));
    }
  };
  walk(resolved, '$', '$', false, 0);
  return { fields, truncated };
}

export function schemaFields(document: OpenApiDocument, schema: unknown): ApiSchemaField[] { return schemaFieldResult(document, schema).fields; }

function examples(document: OpenApiDocument, source: Record<string, unknown>, schema?: unknown): ApiExample[] {
  if (Object.hasOwn(source, 'example')) return [{ name: 'default', value: source.example }];
  const named = object(source.examples);
  if (named) return Object.entries(named).flatMap(([name, raw]) => {
    const example = ExampleObject.safeParse(resolveLocalRef(document, raw, { shallow: true }).value).data;
    if (!example || (!Object.hasOwn(example, 'value') && !example.externalValue)) return [];
    return [{ name, ...(Object.hasOwn(example, 'value') ? { value: example.value } : {}), ...(example.summary ? { summary: example.summary } : {}), ...(example.externalValue ? { externalValue: example.externalValue } : {}) }];
  });
  const shape = object(schema);
  if (shape && Object.hasOwn(shape, 'example')) return [{ name: 'default', value: shape.example }];
  if (Array.isArray(shape?.examples)) return shape.examples.map((value, index) => ({ name: String(index + 1), value }));
  return [];
}

function media(document: OpenApiDocument, content: z.infer<typeof ContentSchema> | undefined): ApiMedia[] {
  return Object.entries(content ?? {}).map(([mediaType, item]) => {
    const resolution = resolveLocalRef(document, item.schema);
    const schema = resolution.value;
    const { fields, truncated } = schemaFieldResult(document, schema);
    const issues = resolution.issues.map((issue) => ({ ...issue, path: `$/schema${issue.path.slice(1)}` }));
    for (const [name, example] of Object.entries(item.examples ?? {})) {
      for (const issue of resolveLocalRef(document, example, { shallow: true }).issues) issues.push({ ...issue, path: `$/examples/${name}${issue.path.slice(1)}` });
    }
    return { mediaType, ...(schema === undefined ? {} : { schema }), fields, examples: examples(document, item, schema), isBinary: object(schema)?.format === 'binary' || mediaType === 'application/octet-stream' || /^(image|audio|video)\//.test(mediaType), ...(item.encoding ? { encoding: item.encoding } : {}),
      issues: [...issues, ...(truncated ? [{ code: 'limit' as const, path: '$/schema' }] : [])] };
  });
}

function parameter(document: OpenApiDocument, raw: unknown): ApiParameter | undefined {
  const item = ParameterObject.safeParse(resolveLocalRef(document, raw, { shallow: true }).value).data;
  if (!item) return undefined;
  const content = media(document, item.content);
  const schema = item.schema === undefined ? content[0]?.schema : resolveLocalRef(document, item.schema).value;
  return { name: item.name, in: item.in, required: item.in === 'path' || item.required === true, description: item.description ?? '', deprecated: item.deprecated === true, type: schemaType(schema), schema, examples: content.length ? content.flatMap((value) => value.examples) : examples(document, item, schema),
    ...(item.style === undefined ? {} : { style: item.style }), ...(item.explode === undefined ? {} : { explode: item.explode }), ...(item.allowReserved === undefined ? {} : { allowReserved: item.allowReserved }) };
}

export function getParameters(document: OpenApiDocument, endpoint: Pick<Endpoint, 'method' | 'path'>): ApiParameter[] {
  const op = getOperation(document, endpoint);
  if (!op) return [];
  const merged = new Map<string, ApiParameter>();
  for (const raw of [...(pathItem(document, endpoint.path)?.parameters ?? []), ...(op.parameters ?? [])]) {
    const item = parameter(document, raw);
    if (item) merged.set(`${item.in}:${item.name}`, item);
  }
  return [...merged.values()];
}

export function getRequest(document: OpenApiDocument, operation: OpenApiOperation): ApiRequestBody | undefined {
  if (!operation.requestBody) return undefined;
  const request = RequestBodyObject.safeParse(resolveLocalRef(document, operation.requestBody, { shallow: true }).value).data;
  return request ? { required: request.required === true, description: request.description ?? '', content: media(document, request.content) } : undefined;
}

export function getResponses(document: OpenApiDocument, operation: OpenApiOperation): ApiResponse[] {
  return Object.entries(operation.responses ?? {}).map(([status, raw]) => {
    const response = ResponseObject.safeParse(resolveLocalRef(document, raw, { shallow: true }).value).data;
    return { status, description: response?.description ?? '', headers: Object.entries(response?.headers ?? {}).flatMap(([name, header]) => {
      const item = parameter(document, { ...object(resolveLocalRef(document, header, { shallow: true }).value), name, in: 'header' });
      return item ? [item] : [];
    }), content: media(document, response?.content) };
  });
}

function messageExamples(value: unknown): ApiExample[] {
  if (value === undefined) return [];
  if (typeof value !== 'string') return [{ name: 'message', value }];
  try { return [{ name: 'message', value: JSON.parse(value) as unknown }]; } catch { return [{ name: 'message', value }]; }
}

export function buildApiCatalogue(document: OpenApiDocument, registry: readonly Section[] = sections): ApiEntry[] {
  const entries = new Map<string, ApiEntry>();
  const messages = new Map((document['x-websocket-messages'] ?? []).map((message) => [message.type, message]));
  const add = (endpoint: Endpoint, section: Pick<Section, 'id' | 'title' | 'description' | 'subHeader'>) => {
    const kind = endpoint.method === 'WS' ? 'websocket' : 'http';
    const specPath = normalizeApiPath(endpoint.path);
    const key = `${endpoint.method} ${specPath}`;
    const operation = getOperation(document, endpoint);
    const messageType = kind === 'websocket' ? endpoint.path.replace(/^→\s*type:\s*/, '') : undefined;
    const message = messageType === undefined ? undefined : messages.get(messageType);
    entries.set(key, {
      key, method: endpoint.method, path: endpoint.path, specPath, groupId: section.id, group: section.title,
      groupDescription: section.description ?? '', summary: message?.summary ?? operation?.summary ?? endpoint.summary,
      description: operation?.description ?? endpoint.description ?? '', deprecated: operation?.deprecated ?? endpoint.deprecated ?? false,
      kind, available: kind === 'websocket' || operation !== undefined,
      ...(messageType === undefined ? {} : { messageType }),
      examples: kind === 'websocket' ? messageExamples(message && Object.hasOwn(message, 'example') ? message.example : endpoint.response) : [],
      ...(section.subHeader ? { subHeader: section.subHeader } : {}),
    });
  };
  for (const section of registry) for (const endpoint of section.endpoints) add(endpoint, section);
  for (const path of Object.keys(document.paths)) {
    for (const method of HTTP_METHODS) {
      if (entries.has(`${method} ${path}`)) continue;
      const operation = getOperation(document, { method, path });
      if (!operation) continue;
      const group = operation?.tags?.[0] ?? 'API';
      const section = registry.find((item) => item.title === group);
      add({ method, path, summary: operation?.summary ?? '' }, section ?? { id: group, title: group, description: document.tags?.find((tag) => tag.name === group)?.description });
    }
  }
  for (const [type, message] of messages) {
    if ([...entries.values()].some((entry) => entry.kind === 'websocket' && entry.messageType === type)) continue;
    add({ method: 'WS', path: `→ type: ${type}`, summary: message.summary ?? '' }, registry.find((section) => section.id === 'websocket') ?? { id: 'websocket', title: 'WebSocket' });
  }
  return [...entries.values()];
}

export function getOperationDetails(document: OpenApiDocument, entry: ApiEntry): ApiOperationDetails | undefined {
  if (entry.kind === 'websocket') return { kind: 'websocket', parameters: [], responses: [], examples: entry.examples, security: [], servers: [], issues: [] };
  const operation = getOperation(document, entry);
  if (!operation) return undefined;
  const requestBody = getRequest(document, operation);
  const responses = getResponses(document, operation);
  const issues = resolveLocalRef(document, { parameters: [...(pathItem(document, entry.path)?.parameters ?? []), ...(operation.parameters ?? [])], requestBody: operation.requestBody, responses: operation.responses }).issues;
  const collectMediaIssues = (content: ApiMedia[], path: string) => {
    for (const medium of content) for (const issue of medium.issues ?? []) issues.push({ ...issue, path: `${path}/${medium.mediaType}${issue.path.slice(1)}` });
  };
  collectMediaIssues(requestBody?.content ?? [], '$/requestBody/content');
  for (const response of responses) collectMediaIssues(response.content, `$/responses/${response.status}/content`);
  return { kind: 'http', ...(operation.operationId ? { operationId: operation.operationId } : {}), parameters: getParameters(document, entry),
    requestBody, responses, examples: [],
    security: operation.security ?? document.security ?? [], servers: operation.servers ?? pathItem(document, entry.path)?.servers ?? document.servers ?? [],
    issues: [...new Map(issues.map((issue) => [JSON.stringify(issue), issue])).values()].slice(0, 100),
  };
}

/* Returns plain text for React text nodes, never HTML for an innerHTML sink. */
export function inlineDocText(text: string | undefined): string {
  const plain = (text ?? '').replace(/<\/?(?:code|strong|em|b|i|p|br)\b[^>]*>/gi, (tag) => /^<\/?(?:p|br)\b/i.test(tag) ? ' ' : '');
  return [...plain].filter((char) => (char.charCodeAt(0) >= 32 && char.charCodeAt(0) !== 127) || '\t\n\r'.includes(char)).join('').trim();
}

export function formatApiExample(value: unknown): string {
  if (typeof value === 'string') return value;
  if (value === undefined) return '';
  try { return JSON.stringify(value, null, 2) ?? ''; } catch { return '[Unserializable example]'; }
}

export const getRequestMetadata = getRequest;
export const getResponseMetadata = getResponses;
export type ApiHttpMethod = HttpMethod;
