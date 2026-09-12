import { useTranslation } from 'react-i18next';
import { Button, Tag, message } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import { PanelCard } from '@/components/ui/panel';
import { ClipboardManager } from '@/utils';
import { formatApiExample, getOperationDetails, inlineDocText, type ApiEntry, type ApiMedia, type ApiParameter, type OpenApiDocument } from './api-document';

const tones: Record<string, string> = { GET: '#4c8dff', POST: '#49b37d', PUT: '#d79d4c', PATCH: '#e4b649', DELETE: '#e66c74', WS: '#9e80d9' };
export function ApiMethod({ method }: { method: string }) {
  const tone = tones[method] || '#8c8f99';
  return <Tag className="api-method" style={{ color: `color-mix(in srgb, ${tone} 60%, var(--p-ink))`, background: `color-mix(in srgb, ${tone} 12%, var(--p-surface))`, borderColor: `color-mix(in srgb, ${tone} 35%, transparent)` }}>{method}</Tag>;
}

export function DocText({ text }: { text?: string }) {
  if (!text) return null;
  return <>{text.split(/(<code>[\s\S]*?<\/code>|`[^`]+`)/g).map((part, index) => {
    if (part.startsWith('<code>') && part.endsWith('</code>')) return <code key={index}>{part.slice(6, -7)}</code>;
    if (part.startsWith('`') && part.endsWith('`')) return <code key={index}>{part.slice(1, -1)}</code>;
    if (!part.trim()) return part;
    return `${part.match(/^\s*/)?.[0] ?? ''}${inlineDocText(part)}${part.match(/\s*$/)?.[0] ?? ''}`;
  })}</>;
}

function ParameterReference({ parameters }: { parameters: ApiParameter[] }) {
  const { t } = useTranslation();
  return <div className="api-reference-fields">{parameters.map(parameter => <div className="api-reference-field" key={`${parameter.in}:${parameter.name}`}>
    <div><code>{parameter.name}</code> <span className="p-sub">{parameter.in} · {parameter.type}</span> {parameter.required && <span className="p-req">{t('apiExplorer.required')}</span>}</div>
    <p><DocText text={parameter.description} /></p>
  </div>)}</div>;
}

function MediaReference({ media }: { media: ApiMedia }) {
  const { t } = useTranslation();
  return <div className="api-media-reference">
    <div className="api-media-label"><code>{media.mediaType}</code>{media.isBinary && <span className="p-sub">{t('apiExplorer.binary')}</span>}</div>
    {!!media.fields.length && <div className="api-reference-fields">{media.fields.map((field, index) => <div className="api-reference-field" key={`${field.path}:${index}`}>
      <div><code dir="ltr">{field.path}</code> <span className="p-sub">{field.type}{field.nullable ? ' | null' : ''}</span> {field.required && <span className="p-req">{t('apiExplorer.required')}</span>}</div>
      {!!field.description && <p><DocText text={field.description} /></p>}
    </div>)}</div>}
    {media.examples.map(example => <div className="api-example" key={example.name}><h4>{t('apiExplorer.example')}{example.summary ? ` · ${example.summary}` : ''}</h4>
      {example.externalValue ? <p className="p-sub">{example.externalValue}</p> : <pre className="p-code" dir="ltr">{formatApiExample(example.value)}</pre>}
    </div>)}
  </div>;
}

export function ApiReference({ entry, document }: { entry: ApiEntry; document: OpenApiDocument }) {
  const { t } = useTranslation();
  const details = getOperationDetails(document, entry);
  if (entry.kind === 'websocket') return <>
    <p className="p-sub">{t('apiExplorer.wsEnvelope')}</p>
    {entry.examples.map(example => <pre className="p-code" dir="ltr" key={example.name}>{formatApiExample(example.value)}</pre>)}
  </>;
  if (!details) return <p className="p-sub">{t('apiExplorer.referenceOnly')}</p>;
  const authentication = details.security.length ? details.security.map(alternative => Object.keys(alternative).map(name => name === 'bearerAuth' ? t('apiExplorer.bearer') : name === 'cookieAuth' ? t('apiExplorer.session') : name).join(' + ') || t('apiExplorer.public')).join(` ${t('apiExplorer.or')} `) : t('apiExplorer.public');
  return <div className="api-reference-sections">
    <p className="p-sub">{t('apiExplorer.authentication')}: {authentication}</p>
    {entry.description && <p><DocText text={entry.description} /></p>}
    {!!details.parameters.length && <section><h3>{t('apiExplorer.parameters')}</h3><ParameterReference parameters={details.parameters} /></section>}
    {!details.parameters.length && !details.requestBody && <p className="p-sub">{t('apiExplorer.noParameters')}</p>}
    {details.requestBody && <section><h3>{t('apiExplorer.requestBody')} {details.requestBody.required && <span className="p-req">{t('apiExplorer.required')}</span>}</h3>
      {!!details.requestBody.description && <p><DocText text={details.requestBody.description} /></p>}
      {details.requestBody.content.map(media => <MediaReference key={media.mediaType} media={media} />)}
    </section>}
    {!!details.responses.length && <section><h3>{t('apiExplorer.documentedResponses')}</h3>{details.responses.map(response => <section key={response.status} className="api-response-reference">
      <h4><code>{response.status}</code> <DocText text={response.description} /></h4>
      {!!response.headers.length && <ParameterReference parameters={response.headers} />}
      {response.content.map(media => <MediaReference key={media.mediaType} media={media} />)}
    </section>)}</section>}
    {!!entry.subHeader?.length && <section><h3>{t('apiExplorer.subscriptionHeaders')}</h3><div className="api-reference-fields">{entry.subHeader.map(header => <div className="api-reference-field" key={header.name}><code>{header.name}</code><p><DocText text={header.desc} /></p></div>)}</div></section>}
    {!!details.issues.length && <p className="p-sub">{t('apiExplorer.partialSchema')}</p>}
  </div>;
}

export function ApiEventReference({ entry, document }: { entry: ApiEntry; document: OpenApiDocument }) {
  const { t } = useTranslation();
  const [messageApi, holder] = message.useMessage();
  const url = new URL(`${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}/ws`, window.location.origin);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  const sample = `const socket = new WebSocket(${JSON.stringify(url.href)});\nsocket.addEventListener('message', ({ data }) => {\n  const event = JSON.parse(data);\n  console.log(event.type, event.payload, event.time);\n});\n// Close when the subscription is no longer needed.\n// socket.close();`;
  return <div className="api-event-reference">{holder}
    <PanelCard pad={0}><div className="p-apihead"><ApiMethod method="WS" /><h2 className="p-apipath">{entry.messageType || '/ws'}</h2></div><div className="api-event-intro"><p><DocText text={entry.summary} /></p><p className="p-sub">{t('apiExplorer.wsSession')}</p></div></PanelCard>
    <PanelCard title={t('apiExplorer.connection')} pad={0} extra={<Button type="text" size="small" icon={<CopyOutlined />} aria-label={t('apiExplorer.copyCode')} onClick={async () => {
      if (await ClipboardManager.copyText(sample)) messageApi.success(t('apiExplorer.copied')); else messageApi.error(t('apiExplorer.copyFailed'));
    }} />}><pre className="p-code" dir="ltr">{sample}</pre></PanelCard>
    <PanelCard title={t('apiExplorer.eventPayload')}><ApiReference entry={entry} document={document} /></PanelCard>
  </div>;
}
