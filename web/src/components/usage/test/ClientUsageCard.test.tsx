// @vitest-environment happy-dom

import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, expect, it, vi } from 'vitest';
import type { UsageClientRecord } from '@/lib/types';
import { ClientUsageCard, type ClientUsageCardProps } from '../ClientUsageCard';

const client: UsageClientRecord = {
  client_ip: '203.0.113.10',
  request_count: 3,
  failure_count: 1,
  failure_rate: 33.333,
  input_tokens: 1000,
  output_tokens: 500,
  reasoning_tokens: 100,
  cache_read_tokens: 200,
  cache_creation_tokens: 50,
  total_tokens: 1500,
  cost_usd: 0.25,
  cost_available: true,
  first_seen_at: '2026-08-10T01:00:00Z',
  last_seen_at: '2026-08-10T02:00:00Z',
  primary_user_agent: 'Codex Desktop/1.0',
  user_agent_count: 2,
};

const mountCard = async (overrides: Partial<ClientUsageCardProps> = {}) => {
  globalThis.IS_REACT_ACT_ENVIRONMENT = true;
  const props: ClientUsageCardProps = {
    clients: [client],
    loading: false,
    page: 1,
    pageSize: 20,
    totalCount: 1,
    totalPages: 1,
    groupBy: 'ip',
    search: '',
    sortBy: 'total_tokens',
    sortOrder: 'desc',
    onPageChange: vi.fn(),
    onPageSizeChange: vi.fn(),
    onGroupByChange: vi.fn(),
    onSearchChange: vi.fn(),
    onSortChange: vi.fn(),
    onInspectClient: vi.fn(),
    ...overrides,
  };
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => root.render(<ClientUsageCard {...props} />));
  return {
    container,
    props,
    unmount: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
};

describe('ClientUsageCard', () => {
  it('shows aggregate metrics and drills into request events by IP', async () => {
    const mounted = await mountCard();
    try {
      const row = mounted.container.querySelector('[data-client-usage-row="203.0.113.10"]');
      expect(row?.textContent).toContain('203.0.113.10');
      expect(row?.textContent).toContain('1.50K');
      expect(row?.textContent).toContain('1 / 33.3%');

      const inspect = mounted.container.querySelector<HTMLButtonElement>('[data-client-usage-inspect="203.0.113.10"]');
      await act(async () => inspect?.click());
      expect(mounted.props.onInspectClient).toHaveBeenCalledWith('203.0.113.10');
    } finally {
      await mounted.unmount();
    }
  });

  it('submits IP search explicitly instead of querying on every keystroke', async () => {
    const onSearchChange = vi.fn();
    const mounted = await mountCard({ onSearchChange });
    try {
      const input = mounted.container.querySelector<HTMLInputElement>('[data-client-usage-search-input="true"]');
      await act(async () => {
        if (!input) return;
        const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
        valueSetter?.call(input, ' 10.0.0 ');
        input.dispatchEvent(new Event('input', { bubbles: true }));
      });
      expect(onSearchChange).not.toHaveBeenCalled();

      const form = mounted.container.querySelector<HTMLFormElement>('[data-client-usage-search-form="true"]');
      await act(async () => form?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
      expect(onSearchChange).toHaveBeenCalledWith('10.0.0');
    } finally {
      await mounted.unmount();
    }
  });
});
