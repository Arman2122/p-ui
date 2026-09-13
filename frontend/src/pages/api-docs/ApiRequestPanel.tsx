import { useEffect, useMemo, useRef, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, Modal, Radio, Select, Space, Tabs, Tag, message } from 'antd';
import { CopyOutlined, DownloadOutlined, SendOutlined, StopOutlined } from '@ant-design/icons';
import { PanelCard } from '@/components/ui/panel';
import { ClipboardManager } from '@/utils';
import type { Endpoint } from './endpoints';
import { getOperation, getParameters, getResponses, formatApiExample, normalizeApiPath, type OpenApiDocument, type ApiParameter } from './api-document';
import { apiDestructive, apiValueKey, initialRequestDraft, prepareApiRequest, requestFields, requestMedia, schemaObject, type ApiRequestDraft } from './request-inputs';
import { apiRequestSnippet, type ApiSnippetLanguage } from './request-snippets';
import { ApiRequestError, managedApiHeader, sendApiRequest, type ApiLiveResponse, type ApiPreparedRequest, type ApiRequestAuth } from './request-transport';
import { ApiMethod, DocText } from './ApiReference';
import { formatApiJson } from './request-json';
import GuidedJsonBody from './GuidedJsonBody';
import { guidedBufferKey, guidedJsonFields, guidedNumberValid, readGuidedJson } from './guided-json';
import './ApiRequestPanel.css';

export interface ApiRequestPanelProps {
  endpoint: Endpoint;
  document: OpenApiDocument;
  auth: ApiRequestAuth;
  basePath?: string;
  draftCache?: Map<string, ApiRequestDraft>;
}
export const apiRequestDraftKey = (endpoint: Endpoint) => `${endpoint.method} ${normalizeApiPath(endpoint.path)}`;
const copyDraft = (draft: ApiRequestDraft): ApiRequestDraft => ({ ...draft, values: { ...draft.values }, files: Object.fromEntries(Object.entries(draft.files).map(([name, files]) => [name, [...files]])) });

export default function ApiRequestPanel(props: ApiRequestPanelProps) {
  return <RequestEditor key={`${props.auth.identityKey ?? ''}:${apiRequestDraftKey(props.endpoint)}`} {...props} />;
}

function RequestEditor({ endpoint, document: apiDocument, auth, basePath, draftCache }: ApiRequestPanelProps) {
  const { t } = useTranslation();
  const [modal, modalContext] = Modal.useModal();
  const [messages, messageContext] = message.useMessage();
  const cacheKey = apiRequestDraftKey(endpoint);
  const form = useForm<ApiRequestDraft>({ defaultValues: { ...copyDraft(draftCache?.get(cacheKey) ?? initialRequestDraft(apiDocument, endpoint)), files: {} } });
  const draft = useWatch({ control: form.control }) as ApiRequestDraft;
  const [language, setLanguage] = useState<ApiSnippetLanguage>('curl');
  const [response, setResponse] = useState<ApiLiveResponse | null>(null);
  const [sentRequest, setSentRequest] = useState<{ method: string; url: string; revision: number } | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [cancelled, setCancelled] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [responseTab, setResponseTab] = useState('live');
  const [jsonView, setJsonView] = useState('formatted');
  const [editorTab, setEditorTab] = useState('');
  const [attempted, setAttempted] = useState(false);
  const [touched, setTouched] = useState<Set<string>>(() => new Set());
  const [focusField, setFocusField] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const pendingRef = useRef<AbortController | null>(null);
  const sequenceRef = useRef(0);
  const draftRevisionRef = useRef(0);
  const mountedRef = useRef(true);
  const confirmationRef = useRef<ReturnType<typeof modal.confirm> | null>(null);
  const operation = getOperation(apiDocument, endpoint);
  const allParams = getParameters(apiDocument, endpoint);
  const params = allParams.filter((param) => param.in !== 'cookie' && !(param.in === 'header' && managedApiHeader(param.name)));
  const media = requestMedia(apiDocument, endpoint);
  const selectedMedia = media.find((item) => item.mediaType === draft.mediaType);
  const bodyFields = requestFields(selectedMedia);
  const jsonMedia = !!selectedMedia && /json/i.test(selectedMedia.mediaType);
  const guidedFields = jsonMedia ? guidedJsonFields(selectedMedia.schema) : undefined;
  const guidedAvailable = !!guidedFields && readGuidedJson(draft.json) !== undefined;
  const pendingGuidedInput = (guidedFields ?? []).some((field) => Object.hasOwn(draft.values, guidedBufferKey(field.name)));
  const bodyMode = draft.values['ui:bodyMode'] === 'json' || !guidedAvailable ? 'json' : 'guided';
  const extraJsonFields = bodyMode === 'guided' ? (readGuidedJson(draft.json) ?? []).filter((member) => !guidedFields?.some((field) => field.name === member.name)).length : 0;
  const prepared = useMemo(() => {
    const input = bodyMode === 'guided' && !draft.json.trim() ? { ...draft, json: '{}' } : draft;
    const result = prepareApiRequest(apiDocument, endpoint, input, auth, basePath);
    const invalid = (guidedFields ?? []).filter((field) => Object.hasOwn(draft.values, guidedBufferKey(field.name)) && !guidedNumberValid(draft.values[guidedBufferKey(field.name)]))
      .map((field) => ({ field: `$.${field.name}`, key: 'apiRepair.request.invalidValue' }));
    return invalid.length ? { issues: [...invalid, ...result.issues], request: undefined } : result;
  }, [apiDocument, endpoint, draft, auth, basePath, guidedFields, bodyMode]);
  const snippet = prepared.request ? apiRequestSnippet(language, prepared.request) : '';
  const responseDisplay = useMemo(() => response?.kind === 'json' && jsonView === 'formatted' ? formatApiJson(response.body) : response?.body, [response, jsonView]);
  const examples = operation ? getResponses(apiDocument, operation).flatMap((item) => item.content.flatMap((content) => content.examples
    .filter((example) => example.value !== undefined).map((example) => ({ key: `${item.status}:${content.mediaType}:${example.name}`, label: `${item.status} · ${content.mediaType} · ${example.name}`, value: example.value })))) : [];
  const [exampleKey, setExampleKey] = useState('');
  const example = examples.find((item) => item.key === exampleKey) ?? examples[0];
  const activeEditorTab = [params.length ? 'parameters' : '', media.length ? 'body' : '', 'code'].includes(editorTab) && editorTab ? editorTab : params.length ? 'parameters' : media.length ? 'body' : 'code';
  const errorFor = (field: string) => attempted || touched.has(field) || (field.startsWith('$.') && touched.has('body'))
    ? prepared.issues.filter((issue) => issue.field === field).map((issue) => t(issue.key)).join(' ') || undefined : undefined;

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; sequenceRef.current += 1; pendingRef.current?.abort(); confirmationRef.current?.destroy(); };
  }, []);

  useEffect(() => {
    pendingRef.current?.abort(); pendingRef.current = null; sequenceRef.current += 1;
    confirmationRef.current?.destroy(); setConfirmed(false);
    setSending(false); setResponse(null); setSentRequest(null); setError(''); setCancelled(false);
  }, [apiDocument, basePath, auth.mode, auth.token, auth.identityKey]);

  useEffect(() => {
    if (!focusField) return;
    const fields = Array.from(rootRef.current?.querySelectorAll<HTMLElement>('[data-api-field]') ?? []);
    const target = fields.find((field) => field.dataset.apiField === focusField) ?? fields.find((field) => field.dataset.apiField === 'body');
    const input = target?.querySelector<HTMLElement>('input:not(:disabled),textarea:not(:disabled),button,[role="combobox"]');
    input?.focus();
    if (input) setFocusField(null);
  }, [focusField, activeEditorTab, bodyMode]);

  function changeDraft(next: ApiRequestDraft, field?: string) {
    const previous = form.getValues();
    const values = (draft: ApiRequestDraft) => Object.entries(draft.values).filter(([key]) => !key.startsWith('ui:'));
    const filesChanged = Object.keys({ ...previous.files, ...next.files }).some((key) => previous.files[key]?.length !== next.files[key]?.length || next.files[key]?.some((file, index) => file !== previous.files[key]?.[index]));
    if (previous.json !== next.json || previous.mediaType !== next.mediaType || JSON.stringify(values(previous)) !== JSON.stringify(values(next)) || filesChanged) draftRevisionRef.current += 1;
    form.reset(next, { keepDefaultValues: true });
    draftCache?.set(cacheKey, { ...copyDraft(next), files: {} });
    if (field) setTouched((previous) => new Set(previous).add(field));
  }
  function change<K extends keyof ApiRequestDraft>(key: K, value: ApiRequestDraft[K], field?: string) { changeDraft({ ...form.getValues(), [key]: value }, field); }
  function editJson(json: string) {
    const values = Object.fromEntries(Object.entries(draft.values).filter(([key]) => !key.startsWith('guided:')));
    changeDraft({ ...draft, json, values }, 'body');
  }
  function selectBodyMode(mode: string) {
    changeDraft({ ...draft, json: mode === 'json' && !draft.json.trim() ? '{}' : draft.json, values: { ...draft.values, 'ui:bodyMode': mode } });
  }
  function resetBody() {
    const values = Object.fromEntries(Object.entries(draft.values).filter(([key]) => !key.startsWith('guided:') && !key.startsWith('body:')));
    rootRef.current?.querySelectorAll<HTMLInputElement>('input[type="file"]').forEach((input) => { input.value = ''; });
    changeDraft({ ...draft, json: '', files: {}, values });
    setAttempted(false); setTouched(new Set());
  }
  function cancel() { pendingRef.current?.abort(); pendingRef.current = null; sequenceRef.current += 1; setSending(false); setCancelled(true); }
  async function copy(text: string) {
    if (await ClipboardManager.copyText(text)) messages.success(t('apiRepair.request.copied'));
    else messages.error(t('apiRepair.request.copyFailed'));
  }

  async function execute(request: ApiPreparedRequest) {
    if (!mountedRef.current || pendingRef.current) return;
    const controller = new AbortController(); pendingRef.current = controller;
    const sequence = ++sequenceRef.current;
    setSentRequest({ method: request.method, url: request.url, revision: draftRevisionRef.current });
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

  function focusIssue(field: string) {
    const param = params.find((item) => item.name === field);
    if (!param && field.startsWith('$.') && guidedFields && !guidedFields.some((item) => `$.${item.name}` === field)) {
      change('values', { ...draft.values, 'ui:bodyMode': 'json' });
    }
    setEditorTab(param ? 'parameters' : media.length ? 'body' : 'code');
    setFocusField(field);
  }
  function send() {
    if (sending || confirmed) return;
    setAttempted(true);
    if (!prepared.request) { if (prepared.issues[0]) focusIssue(prepared.issues[0].field); return; }
    const request = prepared.request;
    if (!apiDestructive(endpoint)) { void execute(request); return; }
    const sequence = sequenceRef.current, revision = draftRevisionRef.current;
    setConfirmed(true);
    confirmationRef.current = modal.confirm({
      title: t('apiRepair.request.confirmTitle'),
      content: <div><p>{t('apiRepair.request.confirmBody')}</p><code>{request.method} {request.url}</code></div>,
      okText: t('apiRepair.request.confirmSend'), okButtonProps: { danger: true }, cancelText: t('cancel'),
      onOk: () => { setConfirmed(false); if (sequence === sequenceRef.current && revision === draftRevisionRef.current) void execute(request); },
      onCancel: () => setConfirmed(false), afterClose: () => setConfirmed(false),
    });
  }

  function downloadResponse() {
    if (!response || response.truncated) return;
    const url = URL.createObjectURL(response.blob), anchor = window.document.createElement('a');
    anchor.href = url; anchor.download = response.kind === 'json' ? 'response.json' : response.kind === 'text' ? 'response.txt' : 'response.bin';
    anchor.click(); window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function fieldControl(param: ApiParameter, location: string) {
    const key = apiValueKey(location, param.name), schema = schemaObject(param.schema), value = draft.values[key] ?? '';
    const secret = schema.format === 'password' || /password|token|secret/i.test(param.name);
    const update = (next: string) => change('values', { ...draft.values, [key]: next }, param.name);
    if (param.type === 'file' || schema.format === 'binary') return <div><input id={`api-file-${encodeURIComponent(param.name)}`} type="file" aria-label={`${location}: ${param.name}`}
      onChange={(event) => change('files', { ...draft.files, [param.name]: Array.from(event.target.files ?? []) }, param.name)} />
      {!!draft.files[param.name]?.length && <span className="p-sub">{draft.files[param.name].map((file) => file.name).join(', ')}</span>}</div>;
    const choices = Array.isArray(schema.enum) ? schema.enum : param.type === 'boolean' ? [true, false] : undefined;
    if (choices) return <Select value={value || undefined} allowClear aria-label={`${location}: ${param.name}`} placeholder={t('apiRepair.request.omitted')} onChange={(next) => update(next ?? '')}
      options={choices.map((item) => ({ value: typeof item === 'object' ? JSON.stringify(item) : String(item), label: JSON.stringify(item) }))} />;
    if (['object', 'array'].includes(param.type)) return <Input.TextArea value={value} onChange={(event) => update(event.target.value)} rows={3} placeholder={param.type === 'array' ? '[]' : '{}'} aria-label={`${location}: ${param.name}`} />;
    return <Input value={value} name={`api-request-${encodeURIComponent(cacheKey)}-${encodeURIComponent(key)}`} onChange={(event) => update(event.target.value)} aria-label={`${location}: ${param.name}`} placeholder={param.type}
      type={secret ? 'password' : 'text'} inputMode={['integer', 'number'].includes(param.type) ? 'decimal' : undefined} autoComplete={secret ? 'new-password' : 'off'}
      data-lpignore="true" data-1p-ignore="true" data-bwignore="true" data-form-type="other" />;
  }
  function parameterRow(param: ApiParameter, location = param.in as string) {
    return <div className="p-paramrow" data-api-field={param.name} key={`${location}:${param.name}`}>
      <div><Space size={6} wrap><span className="p-mono">{param.name}</span><Tag>{location}</Tag><span className="p-sub">{param.type}</span>
        {param.required && <span className="p-req">{t('apiRepair.request.requiredLabel')}</span>}</Space>
        {param.description && <p className="p-sub"><DocText text={param.description} /></p>}</div>
      <Form.Item validateStatus={errorFor(param.name) ? 'error' : undefined} help={errorFor(param.name)}>{fieldControl(param, location)}</Form.Item>
    </div>;
  }
  const genericIssues = prepared.issues.filter((issue) => !params.some((param) => param.name === issue.field) && !bodyFields.some((field) => field.name === issue.field)
    && !(bodyMode === 'guided' && guidedFields?.some((field) => `$.${field.name}` === issue.field)));
  const bodyEditor = <>
    <div className="p-apirequest-body-tools">
      {media.length > 1 ? <Select aria-label={t('apiRepair.request.contentType')} value={draft.mediaType} onChange={(value) => change('mediaType', value)} options={media.map((item) => ({ value: item.mediaType, label: item.mediaType }))} /> : <code className="p-sub">{draft.mediaType}</code>}
      {jsonMedia && guidedFields && <Radio.Group size="small" value={bodyMode} onChange={(event) => selectBodyMode(event.target.value)}>
        <Radio.Button value="guided" disabled={!guidedAvailable}>{t('apiUx.fields')}</Radio.Button><Radio.Button value="json" disabled={pendingGuidedInput} title={pendingGuidedInput ? t('apiRepair.request.invalidValue') : undefined}>JSON</Radio.Button>
      </Radio.Group>}
      {jsonMedia && <>
        {!!selectedMedia?.examples.some((item) => item.value !== undefined) && <Button size="small" onClick={() => editJson(JSON.stringify(selectedMedia!.examples.find((item) => item.value !== undefined)!.value, null, 2))}>{t('apiRepair.request.loadExample')}</Button>}
        <Button size="small" disabled={pendingGuidedInput || !draft.json.trim() || (() => { try { JSON.parse(draft.json); return false; } catch { return true; } })()} onClick={() => editJson(formatApiJson(draft.json))}>{t('apiUx.format')}</Button>
      </>}
      <Button size="small" onClick={resetBody}>{t('reset')}</Button>
    </div>
    {bodyFields.some((field) => field.type === 'file') && <p className="p-apirequest-note">{t('apiUx.reselectFiles')}</p>}
    {!!extraJsonFields && <div className="p-apirequest-note">{t('apiUx.extraFields', { count: extraJsonFields })}<Button type="link" size="small" disabled={pendingGuidedInput} onClick={() => selectBodyMode('json')}>{t('apiUx.openJson')}</Button></div>}
    {jsonMedia ? <div data-api-field="body">
      {bodyMode === 'guided' && guidedFields ? <GuidedJsonBody fields={guidedFields} value={draft.json} buffers={draft.values} errorFor={errorFor}
        onChange={(json, values, field) => changeDraft({ ...draft, json, values }, field)} /> : <div className="p-apirequest-body">
        <p className="p-sub">{t(guidedFields && !guidedAvailable ? 'apiUx.keepInvalidJson' : 'apiUx.jsonHelp')}</p>
        <Input.TextArea value={draft.json} onChange={(event) => editJson(event.target.value)} aria-label={t('apiRepair.request.jsonBody')} autoSize={{ minRows: 8, maxRows: 18 }} spellCheck={false} />
      </div>}
    </div> : <Form component="div">{bodyFields.map((param) => parameterRow(param, 'body'))}</Form>}
  </>;
  const codeEditor = <>
    <div className="p-apirequest-body-tools"><Select aria-label={t('apiRepair.request.language')} value={language} onChange={setLanguage}
      options={['curl', 'JavaScript', 'Python'].map((value) => ({ value, label: value }))} />
      <Button aria-label={t('apiRepair.request.copySnippet')} icon={<CopyOutlined />} disabled={!snippet} onClick={() => void copy(snippet)}>{t('apiUx.copyCode')}</Button></div>
    <pre className="p-code" dir="ltr">{snippet || t('apiRepair.request.completeInputs')}</pre>
    <p className="p-apirequest-note">{t(auth.mode === 'bearer' ? 'apiUx.snippetBearer' : language === 'JavaScript' ? 'apiUx.snippetBrowserSession' : 'apiUx.snippetScriptSession')}
      {draft.mediaType === 'multipart/form-data' && Object.values(draft.files).some((files) => files.length) && <> {t('apiUx.snippetFiles')}</>}
    </p>
  </>;

  return <div className="p-apirequest" ref={rootRef}>
    {modalContext}{messageContext}
    <section className="p-card p-apirequest-heading">
      <div className="p-apirequest-toolbar"><ApiMethod method={endpoint.method} /><code className="p-apirequest-url" dir="ltr">{prepared.request?.url ?? endpoint.path}</code>
        <Button size="small" aria-label={t('apiRepair.request.copyUrl')} icon={<CopyOutlined />} disabled={!prepared.request} onClick={() => void copy(prepared.request?.url ?? '')} />
        <Button type="primary" aria-label={t('apiRepair.request.send')} icon={<SendOutlined />} disabled={confirmed || !operation} loading={sending} onClick={send}>{t('apiRepair.request.send')}</Button>
        {sending && <Button aria-label={t('apiRepair.request.cancel')} icon={<StopOutlined />} onClick={cancel}>{t('apiRepair.request.cancel')}</Button>}
      </div>
    </section>
      <div className="p-apirequest-description"><p><DocText text={endpoint.summary} /></p>{endpoint.description && <details className="p-sub"><summary>{t('apiUx.details')}</summary><p><DocText text={endpoint.description} /></p></details>}
        <span className="p-sub">{t('apiUx.automaticAuth')}</span>
      </div>
    <div className="p-apirequest-workspace">
      <PanelCard className="p-apirequest-editor" title={t('apiRepair.request.request')} pad={0}>
        <Tabs activeKey={activeEditorTab} onChange={setEditorTab} items={[
          ...(params.length ? [{ key: 'parameters', label: t('apiRepair.request.parameters'), children: <Form component="div">{params.map((param) => parameterRow(param))}</Form> }] : []),
          ...(media.length ? [{ key: 'body', label: t('apiRepair.request.body'), children: bodyEditor }] : []),
          { key: 'code', label: t('apiUx.code'), children: codeEditor },
        ]} />
        {genericIssues.filter((issue) => attempted || touched.has(issue.field) || touched.has('body')).slice(0, 6).map((issue, index) => <div className="p-apirequest-inline-issue" key={`${issue.field}:${index}`}>
          <Button type="link" size="small" onClick={() => focusIssue(issue.field)}>{issue.field}</Button><span>{t(issue.key)}</span>
        </div>)}
      </PanelCard>
    <PanelCard className="p-apirequest-response" title={t('apiRepair.request.response')} pad={0} extra={response ? <Space wrap>
      <Tag color={!response.ok ? 'red' : response.envelopeFailed ? 'orange' : 'green'}>{response.status} {response.statusText}</Tag>
      <span>{response.elapsedMs} ms</span><span>{response.size} B</span>
      {response.kind === 'json' && <Radio.Group size="small" value={jsonView} onChange={(event) => setJsonView(event.target.value)}>
        <Radio.Button value="formatted">{t('apiRepair.request.formattedJson')}</Radio.Button>
        <Radio.Button value="raw">{t('apiRepair.request.rawJson')}</Radio.Button>
      </Radio.Group>}
      <Button aria-label={t('apiRepair.request.copyResponse')} icon={<CopyOutlined />} disabled={response.kind === 'binary'} onClick={() => void copy(response.body)} />
      <Button aria-label={t('apiRepair.request.downloadResponse')} icon={<DownloadOutlined />} disabled={response.truncated} onClick={downloadResponse} />
    </Space> : <span className="p-sub">{t(sending ? 'apiRepair.request.sending' : 'apiRepair.request.unsent')}</span>}>
      {sentRequest && <div className="p-apirequest-evidence" role="status">
        <span>{t(sentRequest.revision === draftRevisionRef.current ? 'apiRepair.request.request' : 'apiUx.previousRequest')}</span>
        <code dir="ltr">{sentRequest.method} {sentRequest.url}</code>
      </div>}
      <Tabs activeKey={responseTab} onChange={setResponseTab} items={[
        { key: 'live', label: t('apiUx.responseBody'), children: <>
          {error && <Alert type="error" title={error} showIcon />}
          {cancelled && <Alert type="info" title={t('apiRepair.request.cancelled')} showIcon />}
          {response?.ok && response.envelopeFailed && <Alert type="warning" showIcon title={t('apiRepair.request.envelopeFailed')} />}
          {response?.truncated && <Alert type="warning" showIcon title={t('apiRepair.request.truncated', { bytes: response.size })} />}
          {response ? response.kind === 'binary' ? <p className="p-apirequest-empty">{t('apiRepair.request.binary')}</p>
            : <pre className="p-code" dir="ltr">{responseDisplay || t('apiRepair.request.emptyResponse')}</pre>
            : <p className="p-apirequest-empty">{t(sending ? 'apiRepair.request.sending' : 'apiRepair.request.unsentHint')}</p>}
        </> },
        { key: 'headers', label: t('apiUx.responseHeaders'), children: response ? <pre className="p-code" dir="ltr">{response.headers.map(([name, value]) => `${name}: ${value}`).join('\n')}</pre> : <p className="p-apirequest-empty">{t('apiRepair.request.unsentHint')}</p> },
        { key: 'example', label: t('apiUx.responseExample'), children: <>
          <p className="p-apirequest-note">{t('apiRepair.request.exampleOnly')}</p>
          {example ? <><Select aria-label={t('apiRepair.request.responseExample')} value={example.key} onChange={setExampleKey} options={examples.map((item) => ({ value: item.key, label: item.label }))} />
            <pre className="p-code" dir="ltr">{formatApiExample(example.value)}</pre></> : <p className="p-apirequest-empty">{t('apiRepair.request.noExample')}</p>}
        </> },
      ]} />
    </PanelCard>
    </div>
  </div>;
}
