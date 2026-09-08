import { fireEvent, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { expect, test, vi } from 'vitest';

import PanelSidebar from '@/layouts/PanelSidebar';
import { renderWithProviders } from './test-utils';

function renderSidebar(props: Partial<Parameters<typeof PanelSidebar>[0]> = {}) {
  const onNavigate = vi.fn();
  const view = renderWithProviders(
    <MemoryRouter>
      <PanelSidebar
        route="/clients"
        collapsed={false}
        open={false}
        counts={{}}
        onNavigate={onNavigate}
        {...props}
      />
    </MemoryRouter>,
  );
  return { ...view, onNavigate };
}

test('groups every nav destination under its section heading', () => {
  const { container } = renderSidebar();

  const headings = Array.from(container.querySelectorAll('.p-navgroup')).map((el) => el.textContent);
  expect(headings).toEqual(['Manage', 'Network', 'System', 'Integrate']);

  const labels = Array.from(container.querySelectorAll('.p-navitem-label')).map((el) => el.textContent);
  expect(labels).toEqual([
    'Dashboard',
    'Inbounds', 'Clients', 'Speed Plans', 'Groups',
    'Nodes', 'Hosts', 'Outbounds', 'Routing',
    'Cores', 'Core Configs', 'Panel Settings',
    'API Docs',
  ]);
});

test('marks the current route and reports a click on another one', () => {
  const { container, onNavigate } = renderSidebar();

  const active = container.querySelectorAll('.p-navitem.is-active');
  expect(active).toHaveLength(1);
  expect(active[0].textContent).toContain('Clients');

  fireEvent.click(screen.getByRole('button', { name: /Nodes/ }));
  expect(onNavigate).toHaveBeenCalledWith('/nodes');
});

test('shows a count badge only for the items given one', () => {
  const { container } = renderSidebar({ counts: { cores: 5, plans: 2 } });

  const badges = Array.from(container.querySelectorAll('.p-navbadge')).map((el) => el.textContent);
  expect(badges).toEqual(['2', '5']);
});

/* Collapsed is the icon rail: labels and badges go, the buttons stay. */
test('drops labels when collapsed but keeps every destination reachable', () => {
  const { container } = renderSidebar({ collapsed: true, counts: { cores: 5, plans: 2 } });

  expect(container.querySelectorAll('.p-navitem-label')).toHaveLength(0);
  expect(container.querySelectorAll('.p-navbadge')).toHaveLength(0);
  expect(container.querySelectorAll('.p-navgroup')).toHaveLength(0);
  expect(container.querySelectorAll('nav .p-navitem')).toHaveLength(13);
  expect(container.querySelector('.p-rail')?.classList.contains('is-collapsed')).toBe(true);
});
