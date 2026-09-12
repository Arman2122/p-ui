import { useLayoutEffect } from 'react';
import type { Decorator, Preview } from '@storybook/react-vite';
import { ConfigProvider } from 'antd';
import 'antd/dist/reset.css';
import i18next from 'i18next';
import { initReactI18next } from 'react-i18next';

import { buildAntdThemeConfig } from '@/hooks/useTheme';
import { applyPanelTokens } from '@/hooks/useTokenBridge';
import enUS from '../../internal/web/translation/en-US.json';
import '@/styles/panel.css';
import '@/styles/design-pages.css';

if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    lng: 'en-US',
    fallbackLng: 'en-US',
    resources: { 'en-US': { translation: enUS } },
    interpolation: { escapeValue: false, prefix: '{', suffix: '}' },
    returnNull: false,
  });
}

export const withTheme: Decorator = (Story, context) => {
  const dark = context.globals.theme === 'dark';
  const themeConfig = buildAntdThemeConfig(dark, false);
  useLayoutEffect(() => {
    document.body.classList.remove('dark', 'light');
    document.body.classList.add(dark ? 'dark' : 'light');
    document.documentElement.removeAttribute('data-theme');
    applyPanelTokens(document.documentElement, themeConfig, dark, false);
    document.body.style.background = 'var(--p-bg)';
  }, [dark, themeConfig]);
  return (
    <ConfigProvider theme={themeConfig}>
      <div style={{ padding: 24, minWidth: 320 }}>
        <Story />
      </div>
    </ConfigProvider>
  );
};

const preview: Preview = {
  decorators: [withTheme],
  globalTypes: {
    theme: {
      description: 'Ant Design theme',
      defaultValue: 'light',
      toolbar: {
        title: 'Theme',
        icon: 'circlehollow',
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
  },
  parameters: {
    controls: {
      expanded: true,
      sort: 'requiredFirst',
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    a11y: {
      test: 'error',
    },
  },
};

export default preview;
