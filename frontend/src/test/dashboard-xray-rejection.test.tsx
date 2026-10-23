import { afterEach, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { render, screen, within } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';

import * as statusQuery from '@/api/queries/useStatusQuery';
import { Status } from '@/models/status';
import { ThemeProvider } from '@/hooks/useTheme';
import IndexPage from '@/pages/index/IndexPage';

import { makeTestQueryClient } from './test-utils';

afterEach(() => vi.restoreAllMocks());

it('shows a refused update while Xray stays running and clears it after recovery', () => {
  const rejection = 'config refused: inbounds "a" and "b" both bind tcp port 443';
  const status = new Status({ xray: { state: 'running', errorMsg: rejection } });
  vi.spyOn(statusQuery, 'useStatusQuery').mockReturnValue({
    status, fetched: true, fetchError: '', refresh: vi.fn(),
  });
  const client = makeTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}><ThemeProvider><MemoryRouter>{children}</MemoryRouter></ThemeProvider></QueryClientProvider>
  );
  const view = render(<IndexPage />, { wrapper });

  const alert = screen.getByRole('alert');
  expect(within(alert).getByText(`Xray: ${rejection}`)).toBeTruthy();
  expect(alert.classList.contains('ant-alert-warning')).toBe(true);

  status.xray.errorMsg = '';
  view.rerender(<IndexPage />);
  expect(screen.queryByText(`Xray: ${rejection}`)).toBeNull();
});
