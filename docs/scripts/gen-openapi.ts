import { readFileSync } from 'node:fs';
import { basename } from 'node:path';
import { generateFiles } from 'fumadocs-openapi';
import { openapi } from '../lib/openapi.ts';

// Map a slugified tag name back to its clean display name, so generated page
// titles read "API Tokens" instead of the auto-split "A P I Tokens".
const spec = JSON.parse(readFileSync('./public/openapi.json', 'utf8')) as {
  tags?: { name: string }[];
  'x-websocket-messages'?: { type: string; summary: string; example?: string }[];
};
const titleBySlug = new Map(
  (spec.tags ?? []).map((t) => [t.name.toLowerCase().replace(/\s+/g, '-'), t.name]),
);

// The WebSocket message shapes are not operations — they used to be folded
// into the Paths Object as pseudo-endpoints, which made the document invalid
// OpenAPI — so they are rendered onto the WebSocket page from the vendor
// extension the build script now writes them to.
function websocketMessages(): string {
  const messages = spec['x-websocket-messages'] ?? [];
  if (messages.length === 0) return '';
  const blocks = messages.map((m) => {
    const example = m.example ? `\n\n\`\`\`json\n${m.example}\n\`\`\`` : '';
    return `### \`type: ${m.type}\`\n\n${m.summary}${example}`;
  });
  return `\n\n## Messages\n\nEvery frame is a JSON object whose \`type\` field selects one of these shapes.\n\n${blocks.join('\n\n')}\n`;
}

// Generate one MDX page per tag into the English reference/api folder. Only
// `en` is generated; the Persian copies under content/docs/fa/reference/api are
// maintained by hand, so re-sync them after regenerating (a locale with no copy
// falls back to English).
await generateFiles({
  input: openapi,
  output: './content/docs/en/reference/api',
  per: 'tag',
  beforeWrite(files) {
    for (const file of files) {
      const slug = basename(file.path).replace(/\.mdx$/, '');
      const title = titleBySlug.get(slug);
      if (title) {
        file.content = file.content.replace(/^title:.*$/m, `title: ${title}`);
      }
      if (slug === 'websocket') {
        file.content += websocketMessages();
      }
    }
  },
});

console.log('Generated API reference pages.');
