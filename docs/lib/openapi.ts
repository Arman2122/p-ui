import { createOpenAPI } from 'fumadocs-openapi/server';

// The Penhoon UI OpenAPI spec is committed at public/openapi.json (synced from
// frontend/public/openapi.json elsewhere in this monorepo).
export const openapi = createOpenAPI({
  input: ['./public/openapi.json'],
});
