// Bundle entry for design-sync (not imported by the app). Exports the storied
// components, the antd primitives they compose with, and the preview provider.
import './ds-shim';
import type { ReactNode } from 'react';
import { useLayoutEffect } from 'react';
import { ConfigProvider } from 'antd';
import i18next from 'i18next';
import { initReactI18next } from 'react-i18next';

import { buildAntdThemeConfig } from './src/hooks/useTheme';
import enUS from '../internal/web/translation/en-US.json';

export * from 'antd';

// Re-exported whole so story imports of this module resolve to the bundle:
// a source-bundled copy would own a second ThemeContext identity.
export {
  ThemeProvider,
  useTheme,
  buildAntdThemeConfig,
  pauseAnimationsUntilLeave,
} from './src/hooks/useTheme';

// react-hook-form and the form hook must be the bundle's own copies: a story
// that builds a form from a second copy hands FormField a null context.
export { FormProvider, useForm, useFormContext, useFieldArray, useWatch, Controller } from 'react-hook-form';
export { useZodForm } from './src/components/form/rhf/useZodForm';

// Module-level calendar registry: the story's setDatepicker call must reach the
// same copy the picker subscribes to, or it fetches (404s) and stays gregorian.
export { setDatepicker, useDatepicker } from './src/hooks/useDatepicker';

export { BansTable } from './src/components/clients/BansTable';
export { ClientSpeedTag } from './src/components/clients/ClientSpeedTag';
export { IpLimitControl } from './src/components/clients/IpLimitControl';
export { CoreStateBadge } from './src/components/cores/CoreStateBadge';
export { FormField } from './src/components/form/rhf/FormField';
export { EmailNotifications } from './src/components/ui/notifications/EmailNotifications';
export { NotificationCard } from './src/components/ui/notifications/NotificationCard';
export { NotificationEvent } from './src/components/ui/notifications/NotificationEvent';
export { NotificationGroup } from './src/components/ui/notifications/NotificationGroup';
export { NotificationHeader } from './src/components/ui/notifications/NotificationHeader';
export { NotificationLayout } from './src/components/ui/notifications/NotificationLayout';
export { TelegramNotifications } from './src/components/ui/notifications/TelegramNotifications';

export { default as ClientTrafficCell } from './src/components/clients/ClientTrafficCell';
export { default as ConfigBlock } from './src/components/clients/ConfigBlock';
export { default as PromptModal } from './src/components/feedback/PromptModal';
export { default as TextModal } from './src/components/feedback/TextModal';
export { default as DateTimePicker } from './src/components/form/DateTimePicker';
export { default as HeaderMapEditor } from './src/components/form/HeaderMapEditor';
export { default as JsonEditor } from './src/components/form/JsonEditor';
export { default as RemarkTemplateField } from './src/components/form/RemarkTemplateField';
export { default as RemarkVarPicker } from './src/components/form/RemarkVarPicker';
export { default as SelectAllClearButtons } from './src/components/form/SelectAllClearButtons';
export { default as InfinityIcon } from './src/components/ui/InfinityIcon';
export { default as InputAddon } from './src/components/ui/InputAddon';
export { default as SettingListItem } from './src/components/ui/SettingListItem';
export { default as LazyMount } from './src/components/utility/LazyMount';
export { default as Sparkline } from './src/components/viz/Sparkline';

export const panelLocaleEnUS = enUS;

if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    lng: 'en-US',
    fallbackLng: 'en-US',
    resources: { 'en-US': { translation: enUS } },
    interpolation: { escapeValue: false, prefix: '{', suffix: '}' },
    returnNull: false,
  });
}

// The panel's own chrome: antd theme tokens plus the light/dark body class
// its components read. Mirrors the storybook decorator.
export function PanelProvider({ children }: { children: ReactNode }) {
  useLayoutEffect(() => {
    document.body.classList.remove('dark');
    document.body.classList.add('light');
  }, []);
  return <ConfigProvider theme={buildAntdThemeConfig(false, false)}>{children}</ConfigProvider>;
}
