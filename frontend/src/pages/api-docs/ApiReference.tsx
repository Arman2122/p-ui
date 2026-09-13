import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Select, Tabs, Tag } from 'antd';
import { PanelCard } from '@/components/ui/panel';
import { ApiCodeBlock } from './ApiCodeBlock';
import { formatApiExample, getOperationDetails, inlineDocText, type ApiEntry, type ApiMedia, type ApiParameter, type ApiResponse, type OpenApiDocument } from './api-document';

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
  const [exampleName, setExampleName] = useState(() => media.examples.find(example => example.value !== undefined)?.name ?? media.examples[0]?.name);
  const example = media.examples.find(item => item.name === exampleName) ?? media.examples[0];
  const language = /json/i.test(media.mediaType) ? 'json' : /yaml/i.test(media.mediaType) ? 'yaml' : /xml/i.test(media.mediaType) ? 'xml' : 'text';
  const items = [{ key: 'example', label: t('apiExplorer.example'), children: <>
    {media.examples.length > 1 && <div className="api-example-picker"><span>{t('apiReference.exampleName')}</span><Select aria-label={t('apiReference.exampleName')} value={example?.name} onChange={setExampleName} options={media.examples.map(item => ({ value: item.name, label: item.summary || item.name }))} /></div>}
    {example?.value !== undefined ? <ApiCodeBlock value={language === 'json' ? JSON.stringify(example.value, null, 2) : formatApiExample(example.value)} language={language} label={t('apiExplorer.example')} />
      : example?.externalValue ? <div className="api-reference-empty"><p>{t('apiReference.externalExample')}</p><code dir="ltr">{example.externalValue}</code></div>
        : <p className="p-sub api-reference-empty">{t(media.isBinary ? 'apiReference.binaryExample' : 'apiReference.noExample')}</p>}
    {media.examples.length === 1 && example?.summary && <p className="p-sub api-example-caption"><DocText text={example.summary} /></p>}
  </> }, ...(media.schema !== undefined ? [{ key: 'schema', label: t('apiReference.schema'), children: <ApiCodeBlock value={formatApiExample(media.schema)} language="json" label={t('apiReference.schema')} /> }] : []),
  ...(media.fields.length ? [{ key: 'fields', label: t('apiReference.fields'), children: <div className="api-reference-fields api-schema-fields">{media.fields.map((field, index) => <div className="api-reference-field" key={`${field.path}:${index}`}>
    <div><code dir="ltr">{field.path}</code> <span className="p-sub">{field.type}{field.nullable ? ' | null' : ''}</span> {field.required && <span className="p-req">{t('apiExplorer.required')}</span>}</div>
    {!!field.description && <p><DocText text={field.description} /></p>}
  </div>)}</div> }] : [])];
  return <div className="api-media-reference">
    <Tabs className="api-media-tabs" size="small" destroyOnHidden defaultActiveKey={media.examples.some(item => item.value !== undefined) || media.schema === undefined ? 'example' : 'schema'} items={items} />
  </div>;
}

function ContentReference({ content }: { content: ApiMedia[] }) {
  const { t } = useTranslation();
  const [mediaType, setMediaType] = useState(content[0]?.mediaType);
  const selected = content.find(media => media.mediaType === mediaType) ?? content[0];
  if (!selected) return <p className="p-sub api-reference-empty">{t('apiReference.noBody')}</p>;
  return <>
    <div className="api-media-label"><span className="p-sub">{t('apiRepair.request.contentType')}</span>{content.length > 1 ? <Select aria-label={t('apiRepair.request.contentType')} value={selected.mediaType} onChange={setMediaType} options={content.map(media => ({ value: media.mediaType, label: media.mediaType }))} /> : <code dir="ltr">{selected.mediaType}</code>}{selected.isBinary && <span className="p-sub">{t('apiExplorer.binary')}</span>}</div>
    <MediaReference key={selected.mediaType} media={selected} />
  </>;
}

function ResponseReference({ responses }: { responses: ApiResponse[] }) {
  const { t } = useTranslation();
  return <Tabs className="api-response-tabs" size="small" destroyOnHidden items={responses.map(response => ({ key: response.status,
    label: <code className="api-status-code" data-status={response.status.charAt(0)}>{response.status}</code>,
    children: <div className="api-response-reference">
      <p className="api-response-description"><DocText text={response.description} /></p>
      <ContentReference content={response.content} />
      {!!response.headers.length && <details className="api-reference-headers"><summary>{t('apiReference.responseHeaders')} <span className="p-sub">{response.headers.length}</span></summary><ParameterReference parameters={response.headers} /></details>}
    </div>,
  }))} />;
}

export function ApiReference({ entry, document }: { entry: ApiEntry; document: OpenApiDocument }) {
  const { t } = useTranslation();
  const details = useMemo(() => getOperationDetails(document, entry), [document, entry]);
  if (entry.kind === 'websocket') return <>
    <p className="p-sub">{t('apiExplorer.wsEnvelope')}</p>
    {entry.examples.map(example => <ApiCodeBlock key={example.name} value={formatApiExample(example.value)} language="json" label={t('apiExplorer.eventPayload')} />)}
  </>;
  if (!details) return <p className="p-sub">{t('apiExplorer.referenceOnly')}</p>;
  const authentication = details.security.length ? details.security.map(alternative => Object.keys(alternative).map(name => name === 'bearerAuth' ? t('apiExplorer.bearer') : name === 'cookieAuth' ? t('apiExplorer.session') : name).join(' + ') || t('apiExplorer.public')).join(` ${t('apiExplorer.or')} `) : t('apiExplorer.public');
  return <div className="api-reference-sections">
    <p className="p-sub api-reference-auth"><span>{t('apiExplorer.authentication')}</span> {authentication}</p>
    {entry.description && <p><DocText text={entry.description} /></p>}
    <div className={`api-reference-grid${!details.parameters.length && !details.requestBody ? ' api-reference-grid-response' : ''}`}><div className="api-reference-inputs">
    {!!details.parameters.length && <section className="api-reference-section" role="group" aria-label={t('apiExplorer.parameters')}><h3>{t('apiExplorer.parameters')}</h3><ParameterReference parameters={details.parameters} /></section>}
    {!details.parameters.length && !details.requestBody && <p className="p-sub">{t('apiExplorer.noParameters')}</p>}
    {details.requestBody && <section className="api-reference-section" role="group" aria-label={t('apiExplorer.requestBody')}><h3>{t('apiExplorer.requestBody')} {details.requestBody.required && <span className="p-req">{t('apiExplorer.required')}</span>}</h3>
      {!!details.requestBody.description && <p><DocText text={details.requestBody.description} /></p>}
      <ContentReference content={details.requestBody.content} />
    </section>}
    </div>
    {!!details.responses.length && <section className="api-reference-section api-reference-responses" role="group" aria-label={t('apiExplorer.documentedResponses')}><h3>{t('apiExplorer.documentedResponses')}</h3><ResponseReference responses={details.responses} /></section>}
    </div>
    {!!entry.subHeader?.length && <section><h3>{t('apiExplorer.subscriptionHeaders')}</h3><div className="api-reference-fields">{entry.subHeader.map(header => <div className="api-reference-field" key={header.name}><code>{header.name}</code><p><DocText text={header.desc} /></p></div>)}</div></section>}
    {!!details.issues.length && <p className="p-sub">{t('apiExplorer.partialSchema')}</p>}
  </div>;
}

export function ApiEventReference({ entry, document }: { entry: ApiEntry; document: OpenApiDocument }) {
  const { t } = useTranslation();
  const url = new URL(`${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}/ws`, window.location.origin);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  const sample = `const socket = new WebSocket(${JSON.stringify(url.href)});\nsocket.addEventListener('message', ({ data }) => {\n  const event = JSON.parse(data);\n  console.log(event.type, event.payload, event.time);\n});\n// Close when the subscription is no longer needed.\n// socket.close();`;
  return <div className="api-event-reference">
    <PanelCard pad={0}><div className="p-apihead"><ApiMethod method="WS" /><h2 className="p-apipath">{entry.messageType || '/ws'}</h2></div><div className="api-event-intro"><p><DocText text={entry.summary} /></p><p className="p-sub">{t('apiExplorer.wsSession')}</p></div></PanelCard>
    <ApiCodeBlock value={sample} language="javascript" label={t('apiExplorer.connection')} />
    <PanelCard title={t('apiExplorer.eventPayload')}><ApiReference entry={entry} document={document} /></PanelCard>
  </div>;
}
