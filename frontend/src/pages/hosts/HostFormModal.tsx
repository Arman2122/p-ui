import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Form, Input, InputNumber, Modal, Select, Switch, Tabs, message } from 'antd';
import {
  ProfileOutlined,
  SafetyCertificateOutlined,
  ControlOutlined,
  NodeIndexOutlined,
  SettingOutlined,
  PartitionOutlined,
  DeploymentUnitOutlined,
  RocketOutlined,
  CloudDownloadOutlined,
} from '@ant-design/icons';
import { Controller, FormProvider, useForm, useWatch } from 'react-hook-form';

import type { HostRecord } from '@/api/queries/useHostsQuery';
import { BulkAddHostSchema, type BulkAddHostValues } from '@/schemas/api/host';
import type { InboundOption } from '@/schemas/client';
import { ALPN_OPTION, UTLS_FINGERPRINT } from '@/schemas/primitives';
import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { useNodesQuery } from '@/api/queries/useNodesQuery';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { catTabLabel } from '@/pages/settings/catTabLabel';
import { HostDownloadForm, HostFinalMaskForm, HostMuxForm, HostSockoptForm } from './json-forms';
import './HostFormModal.css';

type FormShape = Omit<BulkAddHostValues, 'isDisabled'> & {
  enable: boolean;
};

interface HostFormModalProps {
  open: boolean;
  mode: 'add' | 'edit';
  host: HostRecord | null;
  inboundOptions: InboundOption[];
  existingHosts: HostRecord[];
  save: (payload: BulkAddHostValues) => Promise<{ success?: boolean; msg?: string } | undefined>;
  onOpenChange: (open: boolean) => void;
}

function namedOptions(label: string, menu: ReactNode) {
  return <div ref={element => { element?.querySelector('[role="listbox"]')?.setAttribute('aria-label', label); }}>{menu}</div>;
}

const asString = (v: unknown): string => (typeof v === 'string' ? v : '');

function defaultsFor(host: HostRecord | null): FormShape {
  return {
    inboundIds: host?.inboundIds ?? [],
    hosts: (host?.hosts || []).filter((h) => h && h.trim() !== ''),
    sortOrder: host?.sortOrder ?? 0,
    remark: host?.remark ?? '',
    serverDescription: host?.serverDescription ?? '',
    enable: host ? !host.isDisabled : true,
    tags: host?.tags ?? [],
    port: host?.port ?? 0,
    security: (host?.security as BulkAddHostValues['security']) ?? 'same',
    sni: host?.sni ?? '',
    hostHeader: host?.hostHeader ?? '',
    path: host?.path ?? '',
    alpn: (host?.alpn as BulkAddHostValues['alpn']) ?? [],
    fingerprint: host?.fingerprint as BulkAddHostValues['fingerprint'],
    overrideSniFromAddress: host?.overrideSniFromAddress ?? false,
    keepSniBlank: host?.keepSniBlank ?? false,
    pinnedPeerCertSha256: host?.pinnedPeerCertSha256 ?? [],
    verifyPeerCertByName: (host?.verifyPeerCertByName as string | undefined) ?? '',
    allowInsecure: host?.allowInsecure ?? false,
    echConfigList: host?.echConfigList ?? '',
    muxParams: asString(host?.muxParams),
    sockoptParams: asString(host?.sockoptParams),
    finalMask: host?.finalMask ?? '',
    downloadSettings: asString(host?.downloadSettings),
    vlessRoute: host?.vlessRoute ?? '',
    excludeFromSubTypes: (host?.excludeFromSubTypes as BulkAddHostValues['excludeFromSubTypes']) ?? [],
    nodeGuids: host?.nodeGuids ?? [],
    mihomoIpVersion: host?.mihomoIpVersion as BulkAddHostValues['mihomoIpVersion'],
    mihomoX25519: host?.mihomoX25519 ?? false,
    shuffleHost: host?.shuffleHost ?? false,
  };
}

export default function HostFormModal({ open, mode, host, inboundOptions, existingHosts, save, onOpenChange }: HostFormModalProps) {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  const methods = useForm<FormShape>({ defaultValues: defaultsFor(host) });
  const [messageApi, messageContextHolder] = message.useMessage();
  const [loading, setLoading] = useState(false);

  const security = (useWatch({ control: methods.control, name: 'security' }) ?? 'same') as string;
  const showTls = security === 'tls' || security === 'reality' || security === 'same';
  const showTlsExtras = security === 'tls' || security === 'same';

  useEffect(() => {
    if (open) {
      methods.reset(defaultsFor(host));
      setLoading(false);
    }
  }, [open, host, methods]);

  const { nodes } = useNodesQuery();

  /* A host rewrites the address a share link carries; a kernel WireGuard
     interface hands out its own config and a host changes nothing in it. */
  const watchedInboundIds = useWatch({ control: methods.control, name: 'inboundIds' });
  const selectedInboundIds = useMemo(() => (watchedInboundIds ?? []) as number[], [watchedInboundIds]);
  const inboundSelectOptions = useMemo(
    () => inboundOptions.map((ib) => {
      const kernelWg = ib.protocol === 'wgkernel' || ib.protocol === 'awgkernel';
      const name = ib.remark || ib.tag || `#${ib.id}`;
      return {
        value: ib.id,
        label: kernelWg ? `${name} (${t('pages.hosts.kernelWgNoEffect')})` : name,
        disabled: kernelWg && !selectedInboundIds.includes(ib.id),
      };
    }),
    [inboundOptions, selectedInboundIds, t],
  );

  const nodeSelectOptions = useMemo(
    () => nodes
      .filter((n) => n.guid)
      .map((n) => ({ value: n.guid as string, label: n.name || n.remark || (n.guid as string) })),
    [nodes],
  );

  const alpnOptions = useMemo(() => Object.values(ALPN_OPTION).map((v) => ({ value: v, label: v })), []);
  const fpOptions = useMemo(() => Object.values(UTLS_FINGERPRINT).map((v) => ({ value: v, label: v })), []);

  const hostOptions = useMemo(() => {
    const addresses = new Set<string>();
    for (const h of existingHosts || []) {
      if (h.hosts) {
        for (const addr of h.hosts) {
          if (addr && addr.trim() !== '') {
            addresses.add(addr);
          }
        }
      }
    }
    return Array.from(addresses).map((addr) => ({ value: addr, label: addr }));
  }, [existingHosts]);

  const onFinish = async (values: FormShape) => {
    if (loading) return;
    const { enable, ...rest } = values;
    const isDisabled = !enable;
    const payload: BulkAddHostValues = {
      ...rest,
      hosts: (rest.hosts || []).filter((h) => h && h.trim() !== ''),
      isDisabled,
    };
    setLoading(true);
    try {
      const res = await save(payload);
      if (res?.success) {
        messageApi.success(t(mode === 'add' ? 'pages.hosts.toasts.add' : 'pages.hosts.toasts.update'));
        onOpenChange(false);
      } else if (res?.msg) {
        messageApi.error(res.msg);
      }
    } catch (err) {
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      open={open}
      className="host-form-modal"
      title={<div><div className="p-h1">{t(mode === 'add' ? 'pages.hosts.addHost' : 'pages.hosts.editHost')}</div><div className="p-sub">{t('designParityHosts.formIntro', { defaultValue: 'Public endpoints advertised in client links and subscriptions.' })}</div></div>}
      onOk={methods.handleSubmit(onFinish)}
      onCancel={() => onOpenChange(false)}
      confirmLoading={loading}
      okText={t('save')}
      cancelText={t('cancel')}
      destroyOnHidden
      width={isMobile ? '95vw' : 680}
      footer={<div className="host-form-footer"><span className="p-sub">{t('designParityHosts.formHint', { defaultValue: 'Overrides apply to the selected inbounds.' })}</span><div><Button onClick={() => onOpenChange(false)} disabled={loading}>{t('cancel')}</Button><Button type="primary" loading={loading} onClick={methods.handleSubmit(onFinish)}>{t('save')}</Button></div></div>}
      styles={{ body: { maxHeight: '70vh', overflowY: 'auto', overflowX: 'hidden' } }}
    >
      {messageContextHolder}
      <FormProvider {...methods}>
        <Form
          layout="vertical"
          colon={false}
          labelWrap
          onFinish={methods.handleSubmit(onFinish)}
        >
          <Tabs
            defaultActiveKey="basic"
            items={[
              {
                key: 'basic',
                forceRender: true,
                label: catTabLabel(<ProfileOutlined aria-hidden />, t('pages.hosts.sections.basic'), isMobile),
                children: (
                  <div className="host-form-grid">
                    <FormField name="remark" label={t('pages.hosts.fields.remark')} tooltip={t('pages.hosts.hints.remark')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.remark) }}>
                      <Input maxLength={256} />
                    </FormField>
                    <FormField name="serverDescription" label={t('pages.hosts.fields.serverDescription')} tooltip={t('pages.hosts.hints.serverDescription')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.serverDescription) }}>
                      <Input maxLength={64} />
                    </FormField>
                    <FormField name="inboundIds" label={t('pages.hosts.fields.inbound')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.inboundIds) }}>
                      <Select popupRender={menu => namedOptions(t('pages.hosts.fields.inbound'), menu)}
                        mode="multiple"
                        options={inboundSelectOptions}
                        showSearch={{ optionFilterProp: 'label' }}
                        placeholder={t('pages.hosts.selectInbound')}
                      />
                    </FormField>
                    <FormField name="hosts" label={t('pages.hosts.fields.address')} tooltip={t('pages.hosts.hints.address')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.hosts) }}>
                      <Select popupRender={menu => namedOptions(t('pages.hosts.fields.address'), menu)}
                        mode="tags"
                        options={hostOptions}
                        tokenSeparators={[',', ';', ' ']}
                        placeholder="cdn.example.com, cdn2.example.com:443"
                      />
                    </FormField>
                    <FormField name="port" label={t('pages.hosts.fields.port')} tooltip={t('pages.hosts.hints.port')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.port) }}>
                      <InputNumber min={0} max={65535} />
                    </FormField>
                    <FormField name="tags" label={t('pages.hosts.fields.tags')} tooltip={t('pages.hosts.hints.tags')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.tags) }}>
                      <Select popupRender={menu => namedOptions(t('pages.hosts.fields.tags'), menu)} mode="tags" allowClear tokenSeparators={[',']} />
                    </FormField>
                    <FormField name="nodeGuids" label={t('pages.hosts.fields.nodeGuids')} tooltip={t('pages.hosts.hints.nodeGuids')}>
                      <Select popupRender={menu => namedOptions(t('pages.hosts.fields.nodeGuids'), menu)} mode="multiple" allowClear options={nodeSelectOptions} showSearch={{ optionFilterProp: 'label' }} />
                    </FormField>
                    <FormField name="enable" label={t('pages.hosts.fields.enable')} valueProp="checked">
                      <Switch />
                    </FormField>
                  </div>
                ),
              },
              {
                key: 'security',
                forceRender: true,
                label: catTabLabel(<SafetyCertificateOutlined aria-hidden />, t('pages.hosts.sections.security'), isMobile),
                children: (
                  <div className="host-form-grid">
                    <FormField name="security" label={t('pages.hosts.fields.security')}>
                      <Select popupRender={menu => namedOptions(t('pages.hosts.fields.security'), menu)}
                        options={['same', 'tls', 'none', 'reality'].map((v) => ({ value: v, label: v }))}
                      />
                    </FormField>
                    {showTls && (
                      <>
                        <FormField name="sni" label={t('pages.hosts.fields.sni')}>
                          <Input />
                        </FormField>
                        <FormField name="overrideSniFromAddress" label={t('pages.hosts.fields.overrideSniFromAddress')} valueProp="checked">
                          <Switch />
                        </FormField>
                        <FormField name="keepSniBlank" label={t('pages.hosts.fields.keepSniBlank')} valueProp="checked">
                          <Switch />
                        </FormField>
                        <FormField name="fingerprint" label={t('pages.hosts.fields.fingerprint')}>
                          <Select popupRender={menu => namedOptions(t('pages.hosts.fields.fingerprint'), menu)} allowClear options={fpOptions} />
                        </FormField>
                      </>
                    )}
                    {showTlsExtras && (
                      <>
                        <FormField name="alpn" label={t('pages.hosts.fields.alpn')}>
                          <Select popupRender={menu => namedOptions(t('pages.hosts.fields.alpn'), menu)} mode="multiple" allowClear options={alpnOptions} />
                        </FormField>
                        <FormField name="pinnedPeerCertSha256" label={t('pages.hosts.fields.pins')}>
                          <Select popupRender={menu => namedOptions(t('pages.hosts.fields.pins'), menu)} mode="tags" allowClear tokenSeparators={[',']} />
                        </FormField>
                        <FormField name="verifyPeerCertByName" label={t('pages.hosts.fields.verifyPeerCertByName')} tooltip={t('pages.inbounds.form.verifyPeerCertByNameTip')}>
                          <Input placeholder="example.com" />
                        </FormField>
                        <FormField name="allowInsecure" label={t('pages.hosts.fields.allowInsecure')} tooltip={t('pages.hosts.hints.allowInsecure')} valueProp="checked">
                          <Switch />
                        </FormField>
                        <FormField name="echConfigList" label={t('pages.hosts.fields.echConfigList')}>
                          <Input.TextArea rows={2} />
                        </FormField>
                      </>
                    )}
                  </div>
                ),
              },
              {
                key: 'advanced',
                forceRender: true,
                label: catTabLabel(<ControlOutlined aria-hidden />, t('pages.hosts.sections.advanced'), isMobile),
                children: (
                  <Tabs
                    size="small"
                    defaultActiveKey="adv-general"
                    items={[
                      {
                        key: 'adv-general',
                        forceRender: true,
                        label: catTabLabel(<SettingOutlined aria-hidden />, t('pages.hosts.sections.general'), isMobile),
                        children: (
                          <>
                            <FormField name="hostHeader" label={t('pages.hosts.fields.hostHeader')} tooltip={t('pages.hosts.hints.hostHeader')}>
                              <Input />
                            </FormField>
                            <FormField name="path" label={t('pages.hosts.fields.path')} tooltip={t('pages.hosts.hints.path')}>
                              <Input />
                            </FormField>
                            <FormField name="vlessRoute" label={t('pages.hosts.fields.vlessRoute')} tooltip={t('pages.hosts.hints.vlessRoute')} rules={{ validate: rhfZodValidate(BulkAddHostSchema.shape.vlessRoute) }}>
                              <Input placeholder="443" />
                            </FormField>
                            <FormField name="excludeFromSubTypes" label={t('pages.hosts.fields.excludeFromSubTypes')}>
                              <Select popupRender={menu => namedOptions(t('pages.hosts.fields.excludeFromSubTypes'), menu)}
                                mode="multiple"
                                allowClear
                                options={['raw', 'json', 'clash'].map((v) => ({ value: v, label: v }))}
                              />
                            </FormField>
                          </>
                        ),
                      },
                      {
                        key: 'adv-mux',
                        forceRender: true,
                        label: catTabLabel(<PartitionOutlined aria-hidden />, t('pages.hosts.fields.muxParams'), isMobile),
                        children: (
                          <Form.Item noStyle>
                            <Controller
                              control={methods.control}
                              name="muxParams"
                              render={({ field }) => (
                                <HostMuxForm value={field.value} onChange={field.onChange} />
                              )}
                            />
                          </Form.Item>
                        ),
                      },
                      {
                        key: 'adv-sockopt',
                        forceRender: true,
                        label: catTabLabel(<DeploymentUnitOutlined aria-hidden />, t('pages.hosts.fields.sockoptParams'), isMobile),
                        children: (
                          <Form.Item noStyle>
                            <Controller
                              control={methods.control}
                              name="sockoptParams"
                              render={({ field }) => (
                                <HostSockoptForm value={field.value} onChange={field.onChange} />
                              )}
                            />
                          </Form.Item>
                        ),
                      },
                      {
                        key: 'adv-finalmask',
                        forceRender: true,
                        label: catTabLabel(<RocketOutlined aria-hidden />, t('pages.hosts.fields.finalMask'), isMobile),
                        children: (
                          <Form.Item noStyle>
                            <Controller
                              control={methods.control}
                              name="finalMask"
                              render={({ field }) => (
                                <HostFinalMaskForm value={field.value} onChange={field.onChange} />
                              )}
                            />
                          </Form.Item>
                        ),
                      },
                      {
                        key: 'adv-download',
                        forceRender: true,
                        label: catTabLabel(<CloudDownloadOutlined aria-hidden />, t('pages.hosts.fields.downloadSettings'), isMobile),
                        children: (
                          <Form.Item noStyle>
                            <Controller
                              control={methods.control}
                              name="downloadSettings"
                              render={({ field }) => (
                                <HostDownloadForm value={field.value} onChange={field.onChange} />
                              )}
                            />
                          </Form.Item>
                        ),
                      },
                    ]}
                  />
                ),
              },
              {
                key: 'clash',
                forceRender: true,
                label: catTabLabel(<NodeIndexOutlined aria-hidden />, t('pages.hosts.sections.clash'), isMobile),
                children: (
                  <>
                    <FormField name="mihomoIpVersion" label={t('pages.hosts.fields.mihomoIpVersion')}>
                      <Select popupRender={menu => namedOptions(t('pages.hosts.fields.mihomoIpVersion'), menu)}
                        allowClear
                        options={['dual', 'ipv4', 'ipv6', 'ipv4-prefer', 'ipv6-prefer'].map((v) => ({ value: v, label: v }))}
                      />
                    </FormField>
                  </>
                ),
              },
            ]}
          />
        </Form>
      </FormProvider>
    </Modal>
  );
}
