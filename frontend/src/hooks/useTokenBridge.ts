import { useLayoutEffect } from 'react';
import { theme as antdTheme } from 'antd';
import type { ThemeConfig } from 'antd';

import { useTheme } from '@/hooks/useTheme';

/* The panel skin (styles/panel.css) is written against --p-* variables so it never
   hardcodes a colour. This is the only place they are derived from the antd theme. */
export function panelTokens(config: ThemeConfig, isDark: boolean, isUltra: boolean): Record<string, string> {
  const tok = antdTheme.getDesignToken(config);
  return {
    '--p-bg': isUltra ? '#000' : isDark ? '#1a1b1f' : tok.colorBgLayout,
    '--p-surface': tok.colorBgContainer,
    '--p-elevated': tok.colorBgElevated,
    '--p-rail': isDark ? (isUltra ? '#050507' : '#15161a') : '#ffffff',
    '--p-border': tok.colorBorderSecondary,
    '--p-border-strong': tok.colorBorder,
    '--p-text': tok.colorText,
    '--p-text-2': tok.colorTextSecondary,
    '--p-text-3': tok.colorTextTertiary,
    '--p-primary': tok.colorPrimary,
    '--p-primary-bg': tok.colorPrimaryBg,
    '--p-fill': tok.colorFillSecondary,
    '--p-fill-q': tok.colorFillQuaternary,
    '--p-success': tok.colorSuccess,
    '--p-warning': tok.colorWarning,
    '--p-error': tok.colorError,
    /* Status colours are tuned for a dark ground. Text drawn in one is mixed
       toward this ink so a chip stays legible on a light tint too. */
    '--p-ink': isDark ? '#ffffff' : '#000000',
    '--p-radius': `${tok.borderRadiusLG}px`,
    '--p-font': tok.fontFamily,
  };
}

export function applyPanelTokens(el: HTMLElement, config: ThemeConfig, isDark: boolean, isUltra: boolean): void {
  const map = panelTokens(config, isDark, isUltra);
  for (const [key, value] of Object.entries(map)) el.style.setProperty(key, value);
}

export function useTokenBridge(): void {
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  useLayoutEffect(() => {
    applyPanelTokens(document.documentElement, antdThemeConfig, isDark, isUltra);
  }, [isDark, isUltra, antdThemeConfig]);
}
