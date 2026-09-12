#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { sections } from '../src/pages/api-docs/endpoints.ts';
import { EXAMPLES } from '../src/generated/examples.ts';
import { SCHEMAS } from '../src/generated/schemas.ts';

const __dirname = dirname(fileURLToPath(import.meta.url));
const outPath = join(__dirname, '..', 'public', 'openapi.json');

const PANEL_VERSION = process.env.PUI_VERSION || '1.x';

const SECURITY_SCHEMES = {
  bearerAuth: {
    type: 'http',
    scheme: 'bearer',
    description: 'API token from Panel Settings → API Token or a reseller account. Send as `Authorization: Bearer <token>`. Role, scope, expiry and source-address restrictions apply.',
  },
  cookieAuth: {
    type: 'apiKey',
    in: 'cookie',
    name: 'p-ui',
    description: 'Session cookie set by POST /login. Browser-only.',
  },
};

function ginPathToOpenApi(path) {
  return path.replace(/:([A-Za-z_][A-Za-z0-9_]*)/g, '{$1}');
}

function extractPathParams(openApiPath) {
  const params = [];
  const re = /\{([A-Za-z_][A-Za-z0-9_]*)\}/g;
  let m;
  while ((m = re.exec(openApiPath)) !== null) params.push(m[1]);
  return params;
}

function mapType(t) {
  const v = String(t || '').toLowerCase();
  if (v === 'number' || v === 'integer' || v === 'int') return 'integer';
  if (v === 'float' || v === 'double') return 'number';
  if (v === 'boolean' || v === 'bool') return 'boolean';
  if (v === 'array') return 'array';
  if (v === 'object') return 'object';
  return 'string';
}

function tryParseJson(raw) {
  if (typeof raw !== 'string') return undefined;
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

function paramToOpenApi(p) {
  const out = {
    name: p.name,
    in: p.in,
    required: p.in === 'path' ? true : !p.optional,
    description: p.desc || '',
    schema: fieldSchema(p.type),
  };
  if (p.defaultValue !== undefined) out.schema.default = p.defaultValue;
  return out;
}

function fieldSchema(type) {
  if (type === 'file') return { type: 'string', format: 'binary' };
  if (type.endsWith('[]')) return { type: 'array', items: { type: mapType(type.slice(0, -2)) } };
  if (type === 'array') return { type: 'array', items: {} };
  return { type: mapType(type) };
}

function requestProperty(property) {
  let schema = structuredClone(property);
  if (schema.$ref && Object.keys(schema).length > 1) {
    const { $ref, ...overrides } = schema;
    const name = $ref.replace('#/components/schemas/', '');
    if (!SCHEMAS[name]) throw new Error(`Unknown request property schema: ${$ref}`);
    const base = structuredClone(SCHEMAS[name]);
    schema = { ...base, ...overrides };
    if (overrides.properties) {
      schema.properties = { ...base.properties };
      for (const [key, value] of Object.entries(overrides.properties)) {
        schema.properties[key] = { ...schema.properties[key], ...value };
      }
    }
  }
  if (schema.items) schema.items = requestProperty(schema.items);
  if (schema.properties) {
    schema.properties = Object.fromEntries(Object.entries(schema.properties).map(([name, value]) => [name, requestProperty(value)]));
  }
  return schema;
}

function buildOperation(ep, tag) {
  const op = {
    tags: [tag],
    summary: ep.summary || '',
    operationId: `${ep.method.toLowerCase()}_${ep.path.replace(/[^A-Za-z0-9]+/g, '_').replace(/^_|_$/g, '')}`,
  };
  if (ep.description) op.description = ep.description;
  if (ep.deprecated) op.deprecated = true;

  const subscription = ep.path.startsWith('/{');
  const publicAuth = ['/login', '/csrf-token', '/getTwoFactorEnable'].includes(ep.path);
  if (subscription || publicAuth) op.security = [];
  if (ep.path === '/logout' || ep.path === '/ws') op.security = [{ cookieAuth: [] }];
  if (subscription) {
    op.servers = [{
      url: '{subscriptionServer}',
      description: 'Separate subscription listener. Set its configured origin before sending a request.',
      variables: { subscriptionServer: { default: 'http://localhost:2096', description: 'Subscription origin, without the panel base path.' } },
    }];
  }

  const params = [];
  const bodyParams = [];
  for (const p of ep.params || []) {
    if (p.in.startsWith('body')) {
      bodyParams.push(p);
    } else if (p.in === 'path' || p.in === 'query' || p.in === 'header') {
      params.push(paramToOpenApi(p));
    }
  }

  const openApiPath = ginPathToOpenApi(ep.path);
  const declared = new Set(params.filter((x) => x.in === 'path').map((x) => x.name));
  for (const name of extractPathParams(openApiPath)) {
    if (declared.has(name)) continue;
    params.push({
      name,
      in: 'path',
      required: true,
      description: '',
      schema: { type: 'string' },
    });
  }

  if (!['GET', 'HEAD', 'OPTIONS', 'TRACE'].includes(ep.method)) {
    params.push({
      name: 'X-CSRF-Token', in: 'header', required: !ep.path.startsWith('/panel/api/'),
      description: 'Required with a session cookie. Fetch /csrf-token under the panel base path and refresh after login. Authenticated API Bearer or mTLS requests bypass this check.',
      schema: { type: 'string' },
    });
  }
  if (params.length > 0) op.parameters = params;

  if (ep.body || ep.requestSchema || ep.requestProperties || bodyParams.length > 0) {
    let example = tryParseJson(ep.body);
    const properties = {};
    const required = [];
    for (const bp of bodyParams) {
      properties[bp.name] = {
        ...fieldSchema(bp.type),
        description: bp.desc || '',
        ...(bp.defaultValue !== undefined ? { default: bp.defaultValue } : {}),
      };
      if (!bp.optional) required.push(bp.name);
    }
    let schema = bodyParams.length > 0
      ? { type: 'object', properties, ...(required.length > 0 ? { required } : {}) }
      : { type: 'object' };

    if (ep.requestSchema) {
      if (!SCHEMAS[ep.requestSchema] || EXAMPLES[ep.requestSchema] === undefined) {
        throw new Error(`${ep.method} ${ep.path}: missing generated request schema ${ep.requestSchema}`);
      }
      schema = bodyParams.length || ep.requestRequired || ep.requestProperties
        ? structuredClone(SCHEMAS[ep.requestSchema]) : { $ref: `#/components/schemas/${ep.requestSchema}` };
      example ??= structuredClone(EXAMPLES[ep.requestSchema]);
    }
    const overrides = { ...properties, ...ep.requestProperties };
    if (Object.keys(overrides).length) {
      schema.properties = { ...schema.properties };
      for (const [name, property] of Object.entries(overrides)) {
        schema.properties[name] = requestProperty({ ...schema.properties[name], ...property });
        if (property.type === 'string') {
          // Some Go string fields store JSON; their request examples must stay strings.
          for (const obj of [schema.properties[name], example]) {
            const key = obj === example ? name : 'example';
            if (obj?.[key] !== null && typeof obj?.[key] === 'object') obj[key] = JSON.stringify(obj[key]);
          }
        }
      }
    }
    if (ep.requestRequired) schema.required = ep.requestRequired;
    if (ep.requestSchemaArray) {
      schema = { type: 'array', items: schema };
      if (!Array.isArray(example)) example = [example];
    }
    const mediaType = bodyParams.some((p) => p.in === 'body (multipart)') ? 'multipart/form-data'
      : bodyParams.some((p) => p.in === 'body (form)') ? 'application/x-www-form-urlencoded' : 'application/json';

    op.requestBody = {
      required: ep.requestBodyRequired ?? (!!ep.requestSchema || required.length > 0 || bodyParams.length === 0),
      content: {
        [mediaType]: {
          schema,
          ...(example !== undefined ? { example } : {}),
        },
      },
    };
  }

  const responses = {};
  let successExample = tryParseJson(ep.response);
  let objSchema = {};
  if (ep.responseSchema) {
    const obj = EXAMPLES[ep.responseSchema];
    if (obj === undefined) {
      throw new Error(`${ep.method} ${ep.path}: responseSchema "${ep.responseSchema}" has no generated example`);
    }
    if (SCHEMAS[ep.responseSchema] === undefined) {
      throw new Error(`${ep.method} ${ep.path}: responseSchema "${ep.responseSchema}" has no generated schema`);
    }
    const ref = { $ref: `#/components/schemas/${ep.responseSchema}` };
    objSchema = ep.responseSchemaArray ? { type: 'array', items: ref } : ref;
    if (successExample === undefined) {
      successExample = { success: true, obj: ep.responseSchemaArray ? [obj] : obj };
    }
  }
  responses['200'] = {
    description: 'Successful response',
    content: {
      'application/json': {
        schema: {
          type: 'object',
          properties: {
            success: { type: 'boolean' },
            msg: { type: 'string' },
            obj: objSchema,
          },
        },
        ...(successExample !== undefined ? { example: successExample } : {}),
      },
    },
  };

  if (ep.responseContent) {
    responses['200'].description = 'Response body without a panel envelope.';
    responses['200'].content = { ...ep.responseContent };
    if (ep.responseErrorEnvelope) {
      responses['200'].description = 'Binary attachment on success; JSON error envelope on failure.';
      responses['200'].content['application/json'] = {
        schema: {
          type: 'object',
          properties: { success: { type: 'boolean', enum: [false] }, msg: { type: 'string' } },
        },
      };
    }
    if (ep.responseContent['application/octet-stream']) {
      responses['200'].headers = {
        'Content-Disposition': { description: 'Attachment filename.', schema: { type: 'string' } },
      };
    }
  }

  if (ep.path === '/ws') {
    delete responses['200'];
    responses['101'] = { description: 'Switching Protocols. An administrator session cookie is required; the server pushes the documented WebSocket messages.' };
  }

  const errExample = tryParseJson(ep.errorResponse);
  if (errExample !== undefined || ep.errorStatus) {
    const code = String(ep.errorStatus || 400);
    if (code === '200' && responses[code]?.content?.['application/json']) {
      const content = responses[code].content['application/json'];
      responses[code].description = 'Application result; inspect success for the outcome.';
      content.examples = {
        ...(successExample !== undefined ? { success: { value: successExample } } : {}),
        ...(errExample !== undefined ? { failure: { value: errExample } } : {}),
      };
      delete content.example;
    } else {
    responses[code] = {
      description: 'Error response',
      content: {
        'application/json': {
          schema: {
            type: 'object',
            properties: {
              success: { type: 'boolean' },
              msg: { type: 'string' },
            },
          },
          ...(errExample !== undefined ? { example: errExample } : {}),
        },
      },
    };
    }
  }

  op.responses = responses;
  return op;
}

export function buildSpec() {
  const paths = {};
  // The WebSocket message shapes are documented as pseudo-endpoints with a
  // `WS` method and a `→ type: …` path. Folded into the Paths Object they made
  // the document invalid OpenAPI; they live in a vendor extension now. F408.
  const messages = [];
  for (const section of sections) {
    const tag = section.title;
    for (const ep of section.endpoints) {
      if (ep.method === 'WS' || !ep.path.startsWith('/')) {
        const message = { type: ep.path.replace(/^→ type:\s*/, ''), summary: ep.summary || '' };
        if (ep.response) message.example = ep.response;
        messages.push(message);
        continue;
      }
      const openApiPath = ginPathToOpenApi(ep.path);
      if (!paths[openApiPath]) paths[openApiPath] = {};
      paths[openApiPath][ep.method.toLowerCase()] = buildOperation(ep, tag);
    }
  }

  const tags = sections.map((s) => ({
    name: s.title,
    description: s.description || '',
  }));

  return {
    openapi: '3.0.3',
    'x-websocket-messages': messages,
    info: {
      title: 'P-UI Panel API',
      version: PANEL_VERSION,
      description:
        'Programmatic interface to a Penhoon UI (P-UI) panel. Authenticate with a session cookie or Bearer token. Administrator operations require administrator access; read-only tokens cannot mutate state or export protected configuration. Reseller tokens are restricted to their own account, clients, granted inbounds and permitted private plans. Token expiry and source-address restrictions also apply.',
    },
    servers: [
      { url: '/', description: 'Current panel (basePath aware)' },
    ],
    components: {
      securitySchemes: SECURITY_SCHEMES,
      schemas: SCHEMAS,
    },
    security: [{ bearerAuth: [] }, { cookieAuth: [] }],
    tags,
    paths,
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const spec = buildSpec();
  writeFileSync(outPath, JSON.stringify(spec, null, 2) + '\n');

  const pathCount = Object.keys(spec.paths).length;
  let opCount = 0;
  for (const ops of Object.values(spec.paths)) opCount += Object.keys(ops).length;
  console.log(`[openapi] wrote ${outPath}`);
  console.log(`[openapi] paths: ${pathCount}, operations: ${opCount}, tags: ${spec.tags.length}`);
}
