import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { EmptyState } from '@/components/ui/EmptyState';
import { Select } from '@/components/ui/Select';
import type { UsageClientGroupBy, UsageClientRecord, UsageClientSortBy, UsageClientSortOrder } from '@/lib/types';
import { formatCompactNumber, formatUsd } from '@/utils/usage';
import styles from './ClientUsageCard.module.scss';

export interface ClientUsageCardProps {
  clients: UsageClientRecord[];
  loading: boolean;
  page: number;
  pageSize: number;
  totalCount: number;
  totalPages: number;
  groupBy: UsageClientGroupBy;
  search: string;
  sortBy: UsageClientSortBy;
  sortOrder: UsageClientSortOrder;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  onGroupByChange: (groupBy: UsageClientGroupBy) => void;
  onSearchChange: (search: string) => void;
  onSortChange: (sortBy: UsageClientSortBy, sortOrder: UsageClientSortOrder) => void;
  onInspectClient: (clientIP: string) => void;
}

const CLIENT_PAGE_SIZES = [20, 50, 100] as const;

const formatClientTimestamp = (value: string) => {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString();
};

export function ClientUsageCard({
  clients,
  loading,
  page,
  pageSize,
  totalCount,
  totalPages,
  groupBy,
  search,
  sortBy,
  sortOrder,
  onPageChange,
  onPageSizeChange,
  onGroupByChange,
  onSearchChange,
  onSortChange,
  onInspectClient,
}: ClientUsageCardProps) {
  const { t } = useTranslation();
  const [searchDraft, setSearchDraft] = useState(search);

  const submitSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onSearchChange(searchDraft.trim());
  };
  const clearSearch = () => {
    setSearchDraft('');
    onSearchChange('');
  };
  const handleSort = (nextSortBy: UsageClientSortBy) => {
    const nextOrder = sortBy === nextSortBy && sortOrder === 'desc' ? 'asc' : 'desc';
    onSortChange(nextSortBy, nextOrder);
  };
  const sortLabel = (key: UsageClientSortBy, label: string) => (
    <button type="button" className={styles.sortButton} onClick={() => handleSort(key)}>
      <span>{label}</span>
      {sortBy === key && <span aria-hidden="true">{sortOrder === 'asc' ? '↑' : '↓'}</span>}
    </button>
  );
  const safePage = totalPages > 0 ? Math.min(Math.max(page, 1), totalPages) : 0;

  return (
    <Card
      className={styles.card}
      variant="flush"
      title={t('usage_stats.clients_title')}
      subtitle={t('usage_stats.clients_subtitle')}
      titleMeta={<span className={styles.countBadge}>{t('usage_stats.clients_count', { count: totalCount })}</span>}
    >
      <div className={styles.notice}>{t('usage_stats.clients_identity_notice')}</div>
      <div className={styles.toolbar}>
        <label className={styles.field}>
          <span>{t('usage_stats.clients_group_by')}</span>
          <Select
            value={groupBy}
            options={[
              { value: 'ip', label: t('usage_stats.clients_group_ip') },
              { value: 'ip_user_agent', label: t('usage_stats.clients_group_ip_user_agent') },
            ]}
            onChange={(value) => onGroupByChange(value as UsageClientGroupBy)}
            ariaLabel={t('usage_stats.clients_group_by')}
            fullWidth={false}
          />
        </label>
        <form className={styles.searchForm} onSubmit={submitSearch} data-client-usage-search-form="true">
          <label className={styles.field}>
            <span>{t('usage_stats.clients_search')}</span>
            <input
              data-client-usage-search-input="true"
              value={searchDraft}
              onChange={(event) => setSearchDraft(event.target.value)}
              maxLength={128}
              placeholder={t('usage_stats.clients_search_placeholder')}
            />
          </label>
          <Button type="submit" size="sm" appearance="action">{t('usage_stats.clients_search_action')}</Button>
          <Button type="button" variant="ghost" size="sm" appearance="action" onClick={clearSearch} disabled={!search && !searchDraft}>
            {t('usage_stats.clear_filters')}
          </Button>
        </form>
      </div>

      {loading && clients.length === 0 ? (
        <div className={styles.hint}>{t('common.loading')}</div>
      ) : clients.length === 0 ? (
        <EmptyState title={t('usage_stats.clients_empty_title')} description={t('usage_stats.clients_empty_desc')} />
      ) : (
        <>
          <div className={styles.tableWrapper}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>{sortLabel('client_ip', t('usage_stats.client_ip'))}</th>
                  {groupBy === 'ip_user_agent' && <th>{t('usage_stats.user_agent')}</th>}
                  <th>{sortLabel('request_count', t('usage_stats.requests_count'))}</th>
                  <th>{sortLabel('failure_rate', t('usage_stats.clients_failures'))}</th>
                  <th>{t('usage_stats.input_tokens')}</th>
                  <th>{t('usage_stats.output_tokens')}</th>
                  <th>{t('usage_stats.reasoning_tokens')}</th>
                  <th>{t('usage_stats.cache_read_tokens')}</th>
                  <th>{t('usage_stats.cache_creation_tokens')}</th>
                  <th>{sortLabel('total_tokens', t('usage_stats.total_tokens'))}</th>
                  <th>{sortLabel('cost_usd', t('usage_stats.total_cost'))}</th>
                  <th>{sortLabel('first_seen_at', t('usage_stats.clients_first_seen'))}</th>
                  <th>{sortLabel('last_seen_at', t('usage_stats.clients_last_seen'))}</th>
                  {groupBy === 'ip' && <th>{t('usage_stats.clients_user_agent_summary')}</th>}
                </tr>
              </thead>
              <tbody>
                {clients.map((client) => (
                  <tr key={`${client.client_ip}\u0000${client.user_agent ?? ''}`} data-client-usage-row={client.client_ip}>
                    <td>
                      <button type="button" className={styles.clientLink} data-client-usage-inspect={client.client_ip} onClick={() => onInspectClient(client.client_ip)}>
                        {client.client_ip}
                      </button>
                    </td>
                    {groupBy === 'ip_user_agent' && <td className={styles.userAgentCell}>{client.user_agent || 'unknown'}</td>}
                    <td>{client.request_count.toLocaleString()}</td>
                    <td>{client.failure_count.toLocaleString()} / {client.failure_rate.toFixed(1)}%</td>
                    <td>{formatCompactNumber(client.input_tokens)}</td>
                    <td>{formatCompactNumber(client.output_tokens)}</td>
                    <td>{formatCompactNumber(client.reasoning_tokens)}</td>
                    <td>{formatCompactNumber(client.cache_read_tokens)}</td>
                    <td>{formatCompactNumber(client.cache_creation_tokens)}</td>
                    <td>{formatCompactNumber(client.total_tokens)}</td>
                    <td>{client.cost_available ? formatUsd(client.cost_usd) : '-'}</td>
                    <td>{formatClientTimestamp(client.first_seen_at)}</td>
                    <td>{formatClientTimestamp(client.last_seen_at)}</td>
                    {groupBy === 'ip' && (
                      <td className={styles.userAgentCell}>
                        <span>{client.primary_user_agent || 'unknown'}</span>
                        <small>{t('usage_stats.clients_user_agent_count', { count: client.user_agent_count })}</small>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className={styles.pagination}>
            <label>
              <span>{t('usage_stats.request_events_rows_per_page')}</span>
              <select value={pageSize} onChange={(event) => onPageSizeChange(Number(event.target.value))} disabled={loading}>
                {CLIENT_PAGE_SIZES.map((size) => <option key={size} value={size}>{size}</option>)}
              </select>
            </label>
            <button type="button" onClick={() => onPageChange(page - 1)} disabled={loading || safePage <= 1}>{t('usage_stats.request_events_previous_page')}</button>
            <strong>{safePage} / {totalPages}</strong>
            <button type="button" onClick={() => onPageChange(page + 1)} disabled={loading || totalPages === 0 || safePage >= totalPages}>{t('usage_stats.request_events_next_page')}</button>
          </div>
        </>
      )}
    </Card>
  );
}
