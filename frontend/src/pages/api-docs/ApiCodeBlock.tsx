import { memo, useId, useMemo, useState, type ReactNode } from 'react';
import { Button } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { ClipboardManager } from '@/utils';
import { getMessage } from '@/utils/messageBus';
import './ApiCodeBlock.css';

export interface ApiCodeBlockProps {
  value: string;
  language?: string;
  label?: string;
}

const JSON_TOKEN = /"(?:\\[\s\S]|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b/g;
const MAX_HIGHLIGHT_LENGTH = 32_768;
const MAX_HIGHLIGHT_TOKENS = 512;

function highlightJson(value: string): ReactNode {
  if (value.length > MAX_HIGHLIGHT_LENGTH) return value;
  const nodes: ReactNode[] = [];
  let cursor = 0, count = 0;
  for (const match of value.matchAll(JSON_TOKEN)) {
    if (++count > MAX_HIGHLIGHT_TOKENS) return value;
    const start = match.index, token = match[0], end = start + token.length;
    if (start > cursor) nodes.push(value.slice(cursor, start));
    let next = end;
    while (next < value.length && /\s/.test(value[next])) next += 1;
    const kind = token.startsWith('"') ? value[next] === ':' ? 'key' : 'string'
      : token === 'true' || token === 'false' || token === 'null' ? 'literal' : 'number';
    nodes.push(<span key={start} className={`api-code-token-${kind}`}>{token}</span>);
    cursor = end;
  }
  if (cursor < value.length) nodes.push(value.slice(cursor));
  return nodes;
}

function languageName(language: string, plainText: string) {
  const kind = language.trim().toLowerCase().split(';', 1)[0].trim();
  if (kind === 'json' || kind === 'application/json' || kind.endsWith('+json')) return { label: 'JSON', json: true };
  const names: Record<string, string> = {
    js: 'JavaScript', javascript: 'JavaScript', 'application/javascript': 'JavaScript', 'text/javascript': 'JavaScript',
    py: 'Python', python: 'Python', bash: 'Bash', sh: 'Shell', shell: 'Shell', curl: 'cURL',
    yaml: 'YAML', yml: 'YAML', xml: 'XML', html: 'HTML', http: 'HTTP', css: 'CSS',
  };
  return { label: !kind || kind === 'text' || kind === 'plaintext' || kind === 'text/plain' ? plainText : names[kind] ?? language.trim(), json: false };
}

export const ApiCodeBlock = memo(function ApiCodeBlock({ value, language = 'text', label }: ApiCodeBlockProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const [copying, setCopying] = useState(false);
  const mode = languageName(language, t('apiCode.plainText'));
  const content = useMemo(() => mode.json ? highlightJson(value) : value, [value, mode.json]);
  async function copy() {
    if (copying) return;
    setCopying(true);
    try {
      if (await ClipboardManager.copyText(value)) getMessage().success(t('apiExplorer.copied'));
      else getMessage().error(t('apiExplorer.copyFailed'));
    } catch { getMessage().error(t('apiExplorer.copyFailed')); }
    finally { setCopying(false); }
  }
  return <div className="api-code-block">
    <div className="api-code-block-header">
      <span id={titleId} className="api-code-block-title" dir="auto">{label || mode.label}</span>
      {label && <span className="api-code-block-language">{mode.label}</span>}
      <Button type="text" size="small" icon={<CopyOutlined />} aria-label={t('apiExplorer.copyCode')} title={t('apiExplorer.copyCode')}
        loading={copying} onClick={() => void copy()} />
    </div>
    {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The scrollable code region needs keyboard focus. */}
    <pre className="api-code-block-pre" role="group" aria-labelledby={titleId} tabIndex={0} dir="ltr"><code>{content}</code></pre>
  </div>;
});

export default ApiCodeBlock;
