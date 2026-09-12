import { useEffect, useMemo, useRef, useState } from 'react';
import { useForm, useWatch, type PathValue } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, Modal, Radio, Select, Space, Tabs, Tag, message } from 'antd';
import { CopyOutlined, DownloadOutlined, SendOutlined, StopOutlined } from '@ant-design/icons';
import { PanelCard } from '@/components/ui/panel';
import { ClipboardManager } from '@/utils';
import type { Endpoint } from './endpoints';
import { getOperation, getParameters, getResponses, formatApiExample, type OpenApiDocument, type ApiParameter } from './api-document';
import { apiDestructive, apiValueKey, initialRequestDraft, prepareApiRequest, requestFields, requestMedia, schemaObject, type ApiRequestDraft } from './request-inputs';
import { apiRequestSnippet, type ApiSnippetLanguage } from './request-snippets';
import { ApiRequestError, managedApiHeader, sendApiRequest, type ApiLiveResponse, type ApiPreparedRequest, type ApiRequestAuth } from './request-transport';
import './ApiRequestPanel.css';
import { ApiMethod, DocText } from './ApiReference';
import { formatApiJson } from './request-json';

export interface ApiRequestPanelProps {
  endpoint: Endpoint;
  document: OpenApiDocument;
  auth: ApiRequestAuth;
  basePath?: string;
}

export default function ApiRequestPanel(props: ApiRequestPanelProps) {
  const { endpoint, auth } = props;
  return <RequestEditor key={`${endpoint.method}:${endpoint.path}:${auth.identityKey ?? ''}:${auth.mode}:${auth.token ?? ''}`} {...props} />;
}

function RequestEditor({ endpoint, document: apiDocument, auth, basePath }: ApiRequestPanelProps) {
  const { t } = useTranslation();
  const [modal, modalContext] = Modal.useModal();
  const [messages, messageContext] = message.useMessage();
  const form = useForm<ApiRequestDraft>({ defaultValues: initialRequestDraft(apiDocument, endpoint) });
  const draft = useWatch({ control: form.control }) as ApiRequestDraft;
  const [language, setLanguage] = useState<ApiSnippetLanguage>('curl');
  const [response, setResponse] = useState<ApiLiveResponse | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [cancelled, setCancelled] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [responseTab, setResponseTab] = useState('live');
  const [jsonView, setJsonView] = useState('formatted');
  const pendingRef = useRef<AbortController | null>(null);
  const sequenceRef = useRef(0);
  const mountedRef = useRef(true);
  const confirmationRef = useRef<ReturnType<typeof modal.confirm> | null>(null);
  const operation = getOperation(apiDocument, endpoint);
  const params = getParameters(apiDocument, endpoint);
  const media = requestMedia(apiDocument, endpoint);
  const selectedMedia = media.find((item) => item.mediaType === draft.mediaType);
  const bodyFields = requestFields(selectedMedia);
  const prepared = useMemo(() => prepareApiRequest(apiDocument, endpoint, draft, auth, basePath), [apiDocument, endpoint, draft, auth, basePath]);
  const snippet = prepared.request ? apiRequestSnippet(language, prepared.request) : '';
  const responseDisplay = useMemo(() => response?.kind === 'json' && jsonView === 'formatted' ? formatApiJson(response.body) : response?.body, [response, jsonView]);
  const examples = operation ? getResponses(apiDocument, operation).flatMap((item) => item.content.flatMap((content) => content.examples
    .filter((example) => example.value !== undefined).map((example) => ({ key: `${item.status}:${content.mediaType}:${example.name}`, label: `${item.status} · ${content.mediaType} · ${example.name}`, value: example.value })))) : [];
  const [exampleKey, setExampleKey] = useState('');
  const example = examples.find((item) => item.key === exampleKey) ?? examples[0];

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      sequenceRef.current += 1;
      pendingRef.current?.abort();
      confirmationRef.current?.destroy();
    };
  }, []);

  useEffect(() => {
    pendingRef.current?.abort();
    pendingRef.current = null;
    sequenceRef.current += 1;
    setSending(false);
    setResponse(null);
    setError('');
    setCancelled(false);
    form.reset(initialRequestDraft(apiDocument, endpoint));
  }, [apiDocument, endpoint, basePath, form]);

  function change<K extends keyof ApiRequestDraft>(key: K, value: ApiRequestDraft[K]) {
    pendingRef.current?.abort();
    pendingRef.current = null;
    sequenceRef.current += 1;
    setSending(false);
    setResponse(null);
    setError('');
    setCancelled(false);
    form.setValue(key, value as PathValue<ApiRequestDraft, K>);
  }

  function cancel() {
    pendingRef.current?.abort();
    pendingRef.current = null;
    sequenceRef.current += 1;
    setSending(false);
    setCancelled(true);
  }

  async function copy(text: string) {
    const copied = await ClipboardManager.copyText(text);
    if (copied) messages.success(t('apiRepair.request.copied'));
    else messages.error(t('apiRepair.request.copyFailed'));
  }

  async function execute(request: ApiPreparedRequest) {
    if (!mountedRef.current || pendingRef.current) return;
    const controller = new AbortController();
    pendingRef.current = controller;
    const sequence = ++sequenceRef.current;
    setSending(true); setError(''); setResponse(null); setCancelled(false); setResponseTab('live');
    try {
      const result = await sendApiRequest(request, controller.signal);
      if (mountedRef.current && sequence === sequenceRef.current) setResponse(result);
    } catch (cause) {
      if (mountedRef.current && sequence === sequenceRef.current) {
        if (controller.signal.aborted) setCancelled(true);
        else setError(cause instanceof ApiRequestError ? `${t(cause.key)}${cause.detail ? ` (${cause.detail})` : ''}` : t('apiRepair.request.networkError'));
      }
    } finally {
      if (pendingRef.current === controller) pendingRef.current = null;
      if (mountedRef.current && sequence === sequenceRef.current) setSending(false);
    }
  }

  function send() {
    if (!prepared.request || sending || confirmed) return;
    const request = prepared.request;
    if (!apiDestructive(endpoint)) { void execute(request); return; }
    setConfirmed(true);
    confirmationRef.current = modal.confirm({
      title: t('apiRepair.request.confirmTitle'),
      content: <div><p>{t('apiRepair.request.confirmBody')}</p><code>{request.method} {request.url}</code></div>,
      okText: t('apiRepair.request.confirmSend'), okButtonProps: { danger: true }, cancelText: t('cancel'),
      onOk: () => { setConfirmed(false); void execute(request); },
      onCancel: () => setConfirmed(false), afterClose: () => setConfirmed(false),
    });
  }

  function downloadResponse() {
    if (!response || response.truncated) return;
    const url = URL.createObjectURL(response.blob);
    const anchor = window.document.createElement('a');
    anchor.href = url; anchor.download = response.kind === 'json' ? 'response.json' : response.kind === 'text' ? 'response.txt' : 'response.bin';
    anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function fieldControl(param: ApiParameter, location: string) {
    const key = apiValueKey(location, param.name), schema = schemaObject(param.schema);
    const value = draft.values?.[key] ?? '';
    const managed = location === 'header' && managedApiHeader(param.name) || location === 'cookie';
    const update = (next: string) => change('values', { ...draft.values, [key]: next });
    if (managed) return <span className="p-sub">{t('apiRepair.request.managedHeader')}</span>;
    if (param.type === 'file' || schema.format === 'binary') return <input id={`api-file-${encodeURIComponent(param.name)}`} type="file" aria-label={`${location}: ${param.name}`}
      onChange={(event) => change('files', { ...draft.files, [param.name]: Array.from(event.target.files ?? []) })} />;
    const choices = Array.isArray(schema.enum) ? schema.enum : param.type === 'boolean' ? [true, false] : undefined;
    if (choices) return <Select value={value || undefined} allowClear style={{ width: '100%' }} aria-label={`${location}: ${param.name}`}
      placeholder={t('apiRepair.request.omitted')} onChange={(next) => update(next ?? '')}
      options={choices.map((item) => ({ value: typeof item === 'object' ? JSON.stringify(item) : String(item), label: JSON.stringify(item) }))} />;
    if (['object', 'array'].includes(param.type)) return <Input.TextArea value={value} onChange={(event) => update(event.target.value)} rows={3} placeholder={param.type === 'array' ? '[]' : '{}'} aria-label={`${location}: ${param.name}`} />;
    return <Input value={value} onChange={(event) => update(event.target.value)} aria-label={`${location}: ${param.name}`} placeholder={param.type}
      type={schema.format === 'password' || /password|token|secret/i.test(param.name) ? 'password' : 'text'} inputMode={['integer', 'number'].includes(param.type) ? 'decimal' : undefined} autoComplete="off" />;
  }

  function parameterRow(param: ApiParameter, location = param.in as string) {
    return <div className="p-paramrow" key={`${location}:${param.name}`}>
      <div><Space size={6} wrap><span className="p-mono">{param.name}</span><Tag>{location}</Tag><span className="p-sub">{param.type}</span>
        {param.required && <span className="p-req">{t('apiRepair.request.requiredLabel')}</span>}</Space>
        {param.description && <p className="p-sub"><DocText text={param.description} /></p>}</div>
      <div>{fieldControl(param, location)}</div>
    </div>;
  }

  return <div className="p-apirequest">
    {modalContext}{messageContext}
    <PanelCard pad={0}>
      <div className="p-apirequest-toolbar"><ApiMethod method={endpoint.method} />
        <code className="p-apirequest-url" dir="ltr">{prepared.request?.url ?? endpoint.path}</code>
        <Button size="small" aria-label={t('apiRepair.request.copyUrl')} icon={<CopyOutlined />} disabled={!prepared.request} onClick={() => void copy(prepared.request?.url ?? '')} />
        <Button type="primary" aria-label={t('apiRepair.request.send')} icon={<SendOutlined />} disabled={!prepared.request || confirmed} loading={sending} onClick={send}>{t('apiRepair.request.send')}</Button>
        {sending && <Button aria-label={t('apiRepair.request.cancel')} icon={<StopOutlined />} onClick={cancel}>{t('apiRepair.request.cancel')}</Button>}
      </div>
      <div className="p-apirequest-description"><p><DocText text={endpoint.summary} /></p>{endpoint.description && <p className="p-sub"><DocText text={endpoint.description} /></p>}
        {!!prepared.issues.length && <ul className="p-apirequest-issues">{prepared.issues.slice(0, 12).map((issue, index) => <li key={`${issue.field}:${index}`}><code>{issue.field}</code>: {t(issue.key)}</li>)}</ul>}
      </div>
    </PanelCard>
    <PanelCard title={t('apiRepair.request.parameters')} pad={0}>
      <Form layout="vertical" component="div">{params.length ? params.map((param) => parameterRow(param)) : <p className="p-apirequest-empty">{t('apiRepair.request.noParameters')}</p>}</Form>
    </PanelCard>
    {!!media.length && <PanelCard title={t('apiRepair.request.body')} pad={0} extra={<Select aria-label={t('apiRepair.request.contentType')}
      value={draft.mediaType} onChange={(value) => change('mediaType', value)} options={media.map((item) => ({ value: item.mediaType, label: item.mediaType }))} />}>
      {selectedMedia && /json/i.test(selectedMedia.mediaType) ? <div className="p-apirequest-body">
        {!!selectedMedia.examples.filter((item) => item.value !== undefined).length && <Button size="small" onClick={() => change('json', JSON.stringify(selectedMedia.examples.find((item) => item.value !== undefined)!.value, null, 2))}>{t('apiRepair.request.loadExample')}</Button>}
        <Input.TextArea value={draft.json ?? ''} onChange={(event) => change('json', event.target.value)} aria-label={t('apiRepair.request.jsonBody')} autoSize={{ minRows: 6, maxRows: 20 }} spellCheck={false} />
      </div> : <Form component="div">{bodyFields.map((param) => parameterRow(param, 'body'))}</Form>}
    </PanelCard>}
    <PanelCard title={t('apiRepair.request.request')} pad={0} extra={<Space><Select aria-label={t('apiRepair.request.language')} value={language}
      onChange={setLanguage} options={['curl', 'JavaScript', 'Python'].map((value) => ({ value, label: value }))} />
      <Button aria-label={t('apiRepair.request.copySnippet')} icon={<CopyOutlined />} disabled={!snippet} onClick={() => void copy(snippet)} /></Space>}>
      <pre className="p-code" dir="ltr">{snippet || t('apiRepair.request.completeInputs')}</pre>
      <p className="p-apirequest-note">{t('apiRepair.request.snippetContext')}</p>
    </PanelCard>
    <PanelCard title={t('apiRepair.request.response')} pad={0} extra={response ? <Space wrap>
      <Tag color={!response.ok ? 'red' : response.envelopeFailed ? 'orange' : 'green'}>{response.status} {response.statusText}</Tag>
      <span>{response.elapsedMs} ms</span><span>{response.size} B</span>
      {response.kind === 'json' && <Radio.Group size="small" value={jsonView} onChange={(event) => setJsonView(event.target.value)}>
        <Radio.Button value="formatted">{t('apiRepair.request.formattedJson')}</Radio.Button>
        <Radio.Button value="raw">{t('apiRepair.request.rawJson')}</Radio.Button>
      </Radio.Group>}
      <Button aria-label={t('apiRepair.request.copyResponse')} icon={<CopyOutlined />} disabled={response.kind === 'binary'} onClick={() => void copy(response.body)} />
      <Button aria-label={t('apiRepair.request.downloadResponse')} icon={<DownloadOutlined />} disabled={response.truncated} onClick={downloadResponse} />
    </Space> : <span className="p-sub">{t('apiRepair.request.unsent')}</span>}>
      <Tabs activeKey={responseTab} onChange={setResponseTab} items={[
        { key: 'live', label: t('apiRepair.request.liveResponse'), children: <>
          {error && <Alert type="error" title={error} showIcon />}
          {cancelled && <Alert type="info" title={t('apiRepair.request.cancelled')} showIcon />}
          {response?.ok && response.envelopeFailed && <Alert type="warning" showIcon title={t('apiRepair.request.envelopeFailed')} />}
          {response?.truncated && <Alert type="warning" showIcon title={t('apiRepair.request.truncated', { bytes: response.size })} />}
          {response ? response.kind === 'binary' ? <p className="p-apirequest-empty">{t('apiRepair.request.binary')}</p>
            : <pre className="p-code" dir="ltr">{responseDisplay || t('apiRepair.request.emptyResponse')}</pre>
            : <p className="p-apirequest-empty">{t(sending ? 'apiRepair.request.sending' : 'apiRepair.request.unsentHint')}</p>}
        </> },
        { key: 'headers', label: t('apiRepair.request.responseHeaders'), children: response ? <pre className="p-code" dir="ltr">{response.headers.map(([name, value]) => `${name}: ${value}`).join('\n')}</pre> : <p className="p-apirequest-empty">{t('apiRepair.request.unsentHint')}</p> },
        { key: 'example', label: t('apiRepair.request.responseExample'), children: <>
          <p className="p-apirequest-note">{t('apiRepair.request.exampleOnly')}</p>
          {example ? <><Select aria-label={t('apiRepair.request.responseExample')} value={example.key} onChange={setExampleKey} options={examples.map((item) => ({ value: item.key, label: item.label }))} />
            <pre className="p-code" dir="ltr">{formatApiExample(example.value)}</pre></> : <p className="p-apirequest-empty">{t('apiRepair.request.noExample')}</p>}
        </> },
      ]} />
    </PanelCard>
  </div>;
}
