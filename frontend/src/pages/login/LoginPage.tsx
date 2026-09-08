import { useCallback, useEffect, useMemo, useState, type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, ConfigProvider, Form, Input, Menu, Popover, Space, Spin, message } from 'antd';
import { TranslationOutlined } from '@ant-design/icons';

import { FormProvider, useForm } from 'react-hook-form';
import { HttpUtil, LanguageManager } from '@/utils';
import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { Icon } from '@/components/ui/Icon';
import { setMessageInstance } from '@/utils/messageBus';
import { useTheme } from '@/hooks/useTheme';
import { applyPanelTokens } from '@/hooks/useTokenBridge';
import { LoginFormSchema, TwoFactorCodeSchema, type LoginFormValues } from '@/schemas/login';
import '@/styles/panel.css';

type LoginForm = LoginFormValues;

const basePath = window.PUI_BASE_PATH || '';

type ThemeChoice = 'light' | 'dark' | 'ultra';

/* The design's pill carries two states; the panel has three themes and the
   sidebar cycles all of them, so dropping ultra here would lose a real setting. */
const THEME_CHOICES: { key: ThemeChoice; labelKey: string; icon: 'sun' | 'moon' }[] = [
  { key: 'light', labelKey: 'pages.login.light', icon: 'sun' },
  { key: 'dark', labelKey: 'pages.login.dark', icon: 'moon' },
  { key: 'ultra', labelKey: 'pages.login.ultra', icon: 'moon' },
];

export default function LoginPage() {
  const { t } = useTranslation();
  const { isDark, isUltra, toggleTheme, toggleUltra, antdThemeConfig } = useTheme();
  const [messageApi, messageContextHolder] = message.useMessage();

  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);

  /* The login bundle has no PanelLayout, so it publishes the skin's tokens itself. */
  useEffect(() => {
    applyPanelTokens(document.documentElement, antdThemeConfig, isDark, isUltra);
  }, [antdThemeConfig, isDark, isUltra]);

  const [fetched, setFetched] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [twoFactorEnable, setTwoFactorEnable] = useState(false);
  const methods = useForm<LoginForm>({ defaultValues: { username: '', password: '', twoFactorCode: '' } });
  const [lang, setLang] = useState<string>(() => LanguageManager.getLanguage());

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const msg = await HttpUtil.post('/getTwoFactorEnable');
      if (cancelled) return;
      if (msg.success) setTwoFactorEnable(!!msg.obj);
      setFetched(true);
    })();
    return () => { cancelled = true; };
  }, []);

  const onSubmit = useCallback(async (values: LoginForm) => {
    setSubmitting(true);
    try {
      const msg = await HttpUtil.post('/login', values);
      if (msg.success) window.location.href = `${basePath}panel/`;
    } finally {
      setSubmitting(false);
    }
  }, []);

  const onLangChange = useCallback((next: string) => {
    setLang(next);
    LanguageManager.setLanguage(next);
  }, []);

  const current: ThemeChoice = !isDark ? 'light' : isUltra ? 'ultra' : 'dark';

  const chooseTheme = useCallback((next: ThemeChoice) => {
    if (next === 'light') {
      if (isUltra) toggleUltra();
      if (isDark) toggleTheme();
      return;
    }
    if (!isDark) toggleTheme();
    if (next === 'ultra' ? !isUltra : isUltra) toggleUltra();
  }, [isDark, isUltra, toggleTheme, toggleUltra]);

  const langMenuItems = useMemo(
    () => (LanguageManager.supportedLanguages as { value: string; name: string; icon: string }[]).map((l) => ({
      key: l.value,
      label: <Space size={8}><span aria-hidden="true">{l.icon}</span><span>{l.name}</span></Space>,
    })),
    [],
  );

  const knobIndex = THEME_CHOICES.findIndex((c) => c.key === current);

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <div className="p-root p-auth">
        <div className="p-auth-bg" aria-hidden="true" />

        {!fetched ? (
          <Spin size="large" />
        ) : (
          <section className="p-auth-card">
            <span className="p-brand-mark p-auth-mark">P</span>
            <h1 className="p-auth-h1">{t('pages.login.signInTitle')}</h1>
            <p className="p-auth-sub">{t('pages.login.subtitle')}</p>

            <FormProvider {...methods}>
              <Form layout="vertical" requiredMark={false} onFinish={methods.handleSubmit(onSubmit)}>
                <FormField
                  name="username"
                  label={t('username')}
                  rules={{ validate: rhfZodValidate(LoginFormSchema.shape.username) }}
                >
                  <Input
                    size="large"
                    autoComplete="username"
                    placeholder={t('username')}
                    autoFocus
                    prefix={<Icon name="user" size={15} style={{ color: 'var(--p-text-3)' }} />}
                  />
                </FormField>

                <FormField
                  name="password"
                  label={t('password')}
                  rules={{ validate: rhfZodValidate(LoginFormSchema.shape.password) }}
                >
                  <Input.Password
                    size="large"
                    autoComplete="current-password"
                    placeholder={t('password')}
                    prefix={<Icon name="lock" size={15} style={{ color: 'var(--p-text-3)' }} />}
                  />
                </FormField>

                {twoFactorEnable && (
                  <FormField
                    name="twoFactorCode"
                    label={t('twoFactorCode')}
                    rules={{ validate: rhfZodValidate(TwoFactorCodeSchema) }}
                  >
                    <Input
                      size="large"
                      autoComplete="one-time-code"
                      placeholder={t('twoFactorCode')}
                      prefix={<Icon name="shield" size={15} style={{ color: 'var(--p-text-3)' }} />}
                    />
                  </FormField>
                )}

                <Form.Item style={{ marginBottom: 0, marginTop: 22 }}>
                  <Button type="primary" htmlType="submit" size="large" block loading={submitting}>
                    {t('login')}
                  </Button>
                </Form.Item>
              </Form>
            </FormProvider>
          </section>
        )}

        <div className="p-auth-foot">
          <span className="p-sub">{t('pages.login.appearance')}</span>
          <div
            className="p-themepill"
            role="group"
            aria-label={t('menu.theme')}
            style={{ '--p-themepill-n': THEME_CHOICES.length } as CSSProperties}
          >
            <span
              className="p-themepill-knob"
              style={{ transform: `translateX(${knobIndex * 100}%)` }}
            />
            {THEME_CHOICES.map((choice) => (
              <button
                type="button"
                key={choice.key}
                className={`p-themepill-b${current === choice.key ? ' is-on' : ''}`}
                aria-pressed={current === choice.key}
                onClick={() => chooseTheme(choice.key)}
              >
                <Icon name={choice.icon} size={14} />
                <span>{t(choice.labelKey)}</span>
              </button>
            ))}
          </div>
          <Popover
            rootClassName={isDark ? 'dark' : 'light'}
            placement="topRight"
            trigger="click"
            styles={{ content: { padding: 4 } }}
            content={
              <Menu
                mode="vertical"
                selectable
                selectedKeys={[lang]}
                items={langMenuItems}
                onClick={({ key }) => onLangChange(key)}
                style={{ border: 'none', minWidth: 160 }}
              />
            }
          >
            <Button shape="circle" aria-label={t('pages.settings.language')} icon={<TranslationOutlined />} />
          </Popover>
        </div>
      </div>
    </ConfigProvider>
  );
}
