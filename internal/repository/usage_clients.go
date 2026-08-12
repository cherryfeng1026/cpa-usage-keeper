package repository

import (
	"fmt"
	"sort"
	"strings"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/repository/dto"
	"cpa-usage-keeper/internal/timeutil"
	"gorm.io/gorm"
)

const (
	usageClientIPSQL        = "CASE WHEN TRIM(COALESCE(client_ip, '')) = '' THEN 'unknown' ELSE TRIM(client_ip) END"
	usageClientUserAgentSQL = "CASE WHEN TRIM(COALESCE(user_agent, '')) = '' THEN 'unknown' ELSE TRIM(user_agent) END"
)

type usageClientAggregateRow struct {
	ClientIP            string `gorm:"column:client_ip"`
	UserAgent           string `gorm:"column:user_agent"`
	APIGroupKey         string `gorm:"column:api_group_key"`
	Model               string `gorm:"column:model"`
	AuthIndex           string `gorm:"column:auth_index"`
	ModelAlias          string `gorm:"column:model_alias"`
	ServiceTier         string `gorm:"column:service_tier"`
	ResponseServiceTier string `gorm:"column:response_service_tier"`
	ReasoningEffort     string `gorm:"column:reasoning_effort"`
	Endpoint            string `gorm:"column:endpoint"`
	ExecutorType        string `gorm:"column:executor_type"`
	RequestCount        int64  `gorm:"column:request_count"`
	FailureCount        int64  `gorm:"column:failure_count"`
	InputTokens         int64  `gorm:"column:input_tokens"`
	OutputTokens        int64  `gorm:"column:output_tokens"`
	ReasoningTokens     int64  `gorm:"column:reasoning_tokens"`
	CacheReadTokens     int64  `gorm:"column:cache_read_tokens"`
	CacheCreationTokens int64  `gorm:"column:cache_creation_tokens"`
	TotalTokens         int64  `gorm:"column:total_tokens"`
	FirstSeenAt         string `gorm:"column:first_seen_at"`
	LastSeenAt          string `gorm:"column:last_seen_at"`
}

type usageClientAccumulator struct {
	record          dto.UsageClientRecord
	userAgentCounts map[string]int64
}

// ListUsageClientsWithFilter 先在 SQLite 中按客户端、User-Agent 和有效定价维度汇总，
// 再复用统一 Resolver 计价并合并为客户端分页，避免 90 天窗口逐事件跨进程扫描。
func ListUsageClientsWithFilter(db *gorm.DB, filter dto.UsageQueryFilter, costResolver pricing.Resolver) (*dto.UsageClientsPageRecord, error) {
	if db == nil {
		return nil, fmt.Errorf("database is nil")
	}
	groupBy := strings.TrimSpace(filter.ClientGroupBy)
	if groupBy == "" {
		groupBy = "ip"
	}
	if groupBy != "ip" && groupBy != "ip_user_agent" {
		return nil, fmt.Errorf("unsupported client group_by %q", groupBy)
	}

	rows, err := loadUsageClientAggregateRows(db, filter, costResolver.ActiveFields())
	if err != nil {
		return nil, err
	}
	totals := make(map[string]*usageClientAccumulator)
	for _, row := range rows {
		firstSeenAt, err := timeutil.ParseStorageTime(row.FirstSeenAt)
		if err != nil {
			return nil, fmt.Errorf("parse client first seen time: %w", err)
		}
		lastSeenAt, err := timeutil.ParseStorageTime(row.LastSeenAt)
		if err != nil {
			return nil, fmt.Errorf("parse client last seen time: %w", err)
		}
		clientIP := normalizeUsageClientValue(row.ClientIP)
		userAgent := normalizeUsageClientValue(row.UserAgent)
		groupKey := clientIP
		if groupBy == "ip_user_agent" {
			groupKey += "\x00" + userAgent
		}
		item := totals[groupKey]
		if item == nil {
			item = &usageClientAccumulator{
				record: dto.UsageClientRecord{
					ClientIP:      clientIP,
					CostAvailable: true,
					FirstSeenAt:   firstSeenAt,
					LastSeenAt:    lastSeenAt,
				},
				userAgentCounts: make(map[string]int64),
			}
			if groupBy == "ip_user_agent" {
				item.record.UserAgent = userAgent
			}
			totals[groupKey] = item
		}

		item.record.RequestCount += row.RequestCount
		item.record.FailureCount += row.FailureCount
		item.record.InputTokens += row.InputTokens
		item.record.OutputTokens += row.OutputTokens
		item.record.ReasoningTokens += row.ReasoningTokens
		item.record.CacheReadTokens += row.CacheReadTokens
		item.record.CacheCreationTokens += row.CacheCreationTokens
		item.record.TotalTokens += row.TotalTokens
		cost := costResolver.Calculate(newUsagePricingCostSubject(
			row.APIGroupKey, row.Model, row.AuthIndex, row.ModelAlias,
			row.ServiceTier, row.ResponseServiceTier, row.ReasoningEffort, row.Endpoint, row.ExecutorType,
			row.InputTokens, row.OutputTokens, row.CacheReadTokens, row.CacheCreationTokens,
		))
		item.record.CostUSD += cost.Cost.TotalCostUSD
		if !cost.Available {
			item.record.CostAvailable = false
		}
		if firstSeenAt.Before(item.record.FirstSeenAt) {
			item.record.FirstSeenAt = firstSeenAt
		}
		if lastSeenAt.After(item.record.LastSeenAt) {
			item.record.LastSeenAt = lastSeenAt
		}
		item.userAgentCounts[userAgent] += row.RequestCount
	}

	clients := make([]dto.UsageClientRecord, 0, len(totals))
	for _, item := range totals {
		if item.record.RequestCount > 0 {
			item.record.FailureRate = float64(item.record.FailureCount) / float64(item.record.RequestCount) * 100
		}
		item.record.UserAgentCount = int64(len(item.userAgentCounts))
		item.record.PrimaryUserAgent = primaryUsageClientUserAgent(item.userAgentCounts)
		clients = append(clients, item.record)
	}
	sortUsageClients(clients, filter.ClientSortBy, filter.ClientSortOrder)
	return paginateUsageClients(clients, filter), nil
}

func loadUsageClientAggregateRows(db *gorm.DB, filter dto.UsageQueryFilter, activeFields pricing.ActiveFields) ([]usageClientAggregateRow, error) {
	dimensions := UsagePricingDimensionColumns(activeFields)
	selectParts := []string{
		usageClientIPSQL + " AS client_ip",
		usageClientUserAgentSQL + " AS user_agent",
	}
	groupParts := []string{usageClientIPSQL, usageClientUserAgentSQL}
	for _, column := range dimensions {
		// 旧迁移添加的部分维度允许 NULL；固定列名来自编译枚举，统一转为空串后交给 Resolver 规范化。
		expression := "COALESCE(" + column + ", '')"
		selectParts = append(selectParts, expression+" AS "+column)
		groupParts = append(groupParts, expression)
	}
	selectParts = append(selectParts,
		"COUNT(*) AS request_count",
		"COALESCE(SUM(CASE WHEN failed THEN 1 ELSE 0 END), 0) AS failure_count",
		"COALESCE(SUM(input_tokens), 0) AS input_tokens",
		"COALESCE(SUM(output_tokens), 0) AS output_tokens",
		"COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens",
		"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens",
		"COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens",
		"COALESCE(SUM(total_tokens), 0) AS total_tokens",
		"MIN(timestamp) AS first_seen_at",
		"MAX(timestamp) AS last_seen_at",
	)
	query := applyUsageClientsQuery(db.Model(&entities.UsageEvent{}), filter).
		Select(strings.Join(selectParts, ", ")).
		Group(strings.Join(groupParts, ", "))
	var rows []usageClientAggregateRow
	if err := query.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("aggregate usage clients: %w", err)
	}
	return rows, nil
}

// applyUsageClientsQuery 只复用时间窗口和全局 API-Key，避免 Request Events 的临时筛选污染客户端聚合。
func applyUsageClientsQuery(query *gorm.DB, filter dto.UsageQueryFilter) *gorm.DB {
	query = applyUsageQueryWindow(query, filter)
	if apiGroupKey := strings.TrimSpace(filter.APIGroupKey); apiGroupKey != "" {
		query = query.Where("api_group_key = ?", apiGroupKey)
	}
	if search := strings.TrimSpace(filter.ClientSearch); search != "" {
		query = query.Where(
			"LOWER("+usageClientIPSQL+") LIKE ? ESCAPE '\\'",
			"%"+escapeUsageClientLike(strings.ToLower(search))+"%",
		)
	}
	return query
}

func normalizeUsageClientValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

func escapeUsageClientLike(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(value)
}

func primaryUsageClientUserAgent(counts map[string]int64) string {
	primary := "unknown"
	var primaryCount int64
	for userAgent, count := range counts {
		if count > primaryCount || (count == primaryCount && userAgent < primary) {
			primary = userAgent
			primaryCount = count
		}
	}
	return primary
}

func sortUsageClients(clients []dto.UsageClientRecord, sortBy, sortOrder string) {
	sortBy = strings.TrimSpace(sortBy)
	if sortBy == "" {
		sortBy = "total_tokens"
	}
	descending := strings.TrimSpace(sortOrder) != "asc"
	sort.SliceStable(clients, func(i, j int) bool {
		left, right := clients[i], clients[j]
		comparison := compareUsageClients(left, right, sortBy)
		if comparison == 0 {
			comparison = strings.Compare(left.ClientIP, right.ClientIP)
			if comparison == 0 {
				comparison = strings.Compare(left.UserAgent, right.UserAgent)
			}
			return comparison < 0
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func compareUsageClients(left, right dto.UsageClientRecord, sortBy string) int {
	switch sortBy {
	case "client_ip":
		return strings.Compare(left.ClientIP, right.ClientIP)
	case "request_count":
		return compareUsageClientInt64(left.RequestCount, right.RequestCount)
	case "failure_count":
		return compareUsageClientInt64(left.FailureCount, right.FailureCount)
	case "failure_rate":
		return compareUsageClientFloat64(left.FailureRate, right.FailureRate)
	case "cost_usd":
		return compareUsageClientFloat64(left.CostUSD, right.CostUSD)
	case "first_seen_at":
		return left.FirstSeenAt.Compare(right.FirstSeenAt)
	case "last_seen_at":
		return left.LastSeenAt.Compare(right.LastSeenAt)
	default:
		return compareUsageClientInt64(left.TotalTokens, right.TotalTokens)
	}
}

func compareUsageClientInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareUsageClientFloat64(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func paginateUsageClients(clients []dto.UsageClientRecord, filter dto.UsageQueryFilter) *dto.UsageClientsPageRecord {
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = filter.Limit
	}
	if pageSize <= 0 {
		pageSize = dto.DefaultUsageClientsLimit
	}
	totalCount := int64(len(clients))
	totalPages := 0
	if totalCount > 0 {
		totalPages = int((totalCount-1)/int64(pageSize) + 1)
	}
	pageIndex := page - 1
	start := len(clients)
	if pageIndex == 0 {
		start = 0
	} else if pageIndex <= len(clients)/pageSize {
		start = pageIndex * pageSize
	}
	end := len(clients)
	if pageSize < len(clients)-start {
		end = start + pageSize
	}
	return &dto.UsageClientsPageRecord{
		Clients: clients[start:end], TotalCount: totalCount, Page: page, PageSize: pageSize, TotalPages: totalPages,
	}
}
