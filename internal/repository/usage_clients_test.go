package repository

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository/dto"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type usageClientsSQLRecorder struct {
	logs strings.Builder
}

func (r *usageClientsSQLRecorder) Printf(message string, args ...interface{}) {
	fmt.Fprintf(&r.logs, message, args...)
	r.logs.WriteByte('\n')
}

func TestListUsageClientsWithFilterAggregatesNormalizedIPAndUserAgents(t *testing.T) {
	db, err := OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "usage-clients.db")})
	if err != nil {
		t.Fatalf("OpenDatabase returned error: %v", err)
	}
	closeTestDatabase(t, db)

	if _, err := UpsertModelPriceSetting(db, dto.ModelPriceSettingInput{
		Model:                "priced-model",
		PricingStyle:         entities.ModelPricingStyleOpenAI,
		PromptPricePer1M:     2,
		CompletionPricePer1M: 4,
	}); err != nil {
		t.Fatalf("UpsertModelPriceSetting returned error: %v", err)
	}

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	ipWithSpace := " 10.0.0.1 "
	ip := "10.0.0.1"
	blankIP := "   "
	literalUnknownIP := " unknown "
	uaA := "client/a"
	uaB := "client/b"
	blankUA := " "
	events := []entities.UsageEvent{
		{EventKey: "client-a-1", APIGroupKey: "key-a", Model: "priced-model", ClientIP: &ipWithSpace, UserAgent: &uaA, Timestamp: start.Add(time.Hour), InputTokens: 1_000_000, TotalTokens: 1_000_000},
		{EventKey: "client-a-2", APIGroupKey: "key-a", Model: "priced-model", ClientIP: &ip, UserAgent: &uaA, Timestamp: start.Add(2 * time.Hour), Failed: true, OutputTokens: 500_000, TotalTokens: 500_000},
		{EventKey: "client-b", APIGroupKey: "key-a", Model: "priced-model", ClientIP: &ip, UserAgent: &uaB, Timestamp: start.Add(3 * time.Hour), ReasoningTokens: 250_000, TotalTokens: 250_000},
		{EventKey: "unknown-null", APIGroupKey: "key-a", Model: "priced-model", Timestamp: start.Add(4 * time.Hour), CacheReadTokens: 200_000, TotalTokens: 200_000},
		{EventKey: "unknown-blank", APIGroupKey: "key-a", Model: "priced-model", ClientIP: &blankIP, UserAgent: &blankUA, Timestamp: start.Add(5 * time.Hour), CacheCreationTokens: 100_000, TotalTokens: 100_000},
		{EventKey: "unknown-literal", APIGroupKey: "key-a", Model: "priced-model", ClientIP: &literalUnknownIP, Timestamp: start.Add(5*time.Hour + time.Minute)},
		{EventKey: "other-key", APIGroupKey: "key-b", Model: "priced-model", ClientIP: &ip, UserAgent: &uaA, Timestamp: start.Add(6 * time.Hour), TotalTokens: 9_000_000},
		{EventKey: "outside-window", APIGroupKey: "key-a", Model: "priced-model", ClientIP: &ip, UserAgent: &uaA, Timestamp: end.Add(time.Hour), TotalTokens: 8_000_000},
	}
	if _, _, err := InsertUsageEvents(db, events); err != nil {
		t.Fatalf("InsertUsageEvents returned error: %v", err)
	}

	page, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{
		StartTime: &start, EndTime: &end, EndExclusive: true,
		APIGroupKey: "key-a", Page: 1, PageSize: 20,
		ClientGroupBy: "ip", ClientSortBy: "total_tokens", ClientSortOrder: "desc",
	}, pricingResolverFromDBForTest(t, db))
	if err != nil {
		t.Fatalf("ListUsageClientsWithFilter returned error: %v", err)
	}
	if page.TotalCount != 2 || page.TotalPages != 1 || len(page.Clients) != 2 {
		t.Fatalf("unexpected client page metadata: %+v", page)
	}

	client := page.Clients[0]
	if client.ClientIP != "10.0.0.1" || client.RequestCount != 3 || client.FailureCount != 1 {
		t.Fatalf("unexpected normalized client aggregate: %+v", client)
	}
	if client.InputTokens != 1_000_000 || client.OutputTokens != 500_000 || client.ReasoningTokens != 250_000 || client.TotalTokens != 1_750_000 {
		t.Fatalf("unexpected client token aggregate: %+v", client)
	}
	if client.PrimaryUserAgent != uaA || client.UserAgentCount != 2 {
		t.Fatalf("unexpected user-agent summary: %+v", client)
	}
	if !client.FirstSeenAt.Equal(start.Add(time.Hour)) || !client.LastSeenAt.Equal(start.Add(3*time.Hour)) {
		t.Fatalf("unexpected first/last seen timestamps: %+v", client)
	}
	if !client.CostAvailable || math.Abs(client.CostUSD-4) > 0.000000001 {
		t.Fatalf("expected per-event pricing to aggregate to $4, got %+v", client)
	}

	unknown := page.Clients[1]
	if unknown.ClientIP != "unknown" || unknown.RequestCount != 3 || unknown.CacheReadTokens != 200_000 || unknown.CacheCreationTokens != 100_000 {
		t.Fatalf("expected null, blank, and literal unknown IPs to share unknown group, got %+v", unknown)
	}
}

func TestListUsageClientsWithFilterPreservesActivePricingDimensions(t *testing.T) {
	db, err := OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "usage-client-pricing-rules.db")})
	if err != nil {
		t.Fatalf("OpenDatabase returned error: %v", err)
	}
	closeTestDatabase(t, db)

	const pricedModel = "priced-by-rules"
	if _, err := UpsertModelPriceSetting(db, dto.ModelPriceSettingInput{
		Model:            pricedModel,
		PromptPricePer1M: 10,
	}); err != nil {
		t.Fatalf("UpsertModelPriceSetting returned error: %v", err)
	}
	if _, err := ReplaceModelPriceRules(db, pricedModel, []dto.ModelPriceRuleInput{
		{Key: "service_tier", Value: "priority", Multiplier: 2},
		{Key: "reasoning_effort", Value: "xhigh", Multiplier: 3},
		{Key: "endpoint", Value: "/v1/responses", Multiplier: 5},
	}); err != nil {
		t.Fatalf("ReplaceModelPriceRules returned error: %v", err)
	}

	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	ip := "198.51.100.24"
	modelAlias := pricedModel
	events := []entities.UsageEvent{
		{
			EventKey: "default-tier", ClientIP: &ip, Model: pricedModel,
			ServiceTier: "default", ReasoningEffort: "medium", Timestamp: now,
			InputTokens: 1_000_000, TotalTokens: 1_000_000,
		},
		{
			EventKey: "priority-xhigh-alias", ClientIP: &ip, Model: "unpriced-source", ModelAlias: &modelAlias,
			ServiceTier: "priority", ReasoningEffort: "xhigh", Endpoint: "/v1/responses", Timestamp: now.Add(time.Minute),
			InputTokens: 1_000_000, TotalTokens: 1_000_000,
		},
	}
	if _, _, err := InsertUsageEvents(db, events); err != nil {
		t.Fatalf("InsertUsageEvents returned error: %v", err)
	}
	// endpoint 是早期迁移添加的 nullable 列，模拟升级前的历史行。
	if err := db.Exec("UPDATE usage_events SET endpoint = NULL WHERE event_key = ?", "default-tier").Error; err != nil {
		t.Fatalf("set historical nullable endpoint: %v", err)
	}

	page, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{Page: 1, PageSize: 20}, pricingResolverFromDBForTest(t, db))
	if err != nil {
		t.Fatalf("ListUsageClientsWithFilter returned error: %v", err)
	}
	if len(page.Clients) != 1 {
		t.Fatalf("expected one merged client, got %+v", page)
	}
	client := page.Clients[0]
	// 历史 NULL endpoint 的基础请求 $10；priority + xhigh + endpoint 的别名请求 $10 * 2 * 3 * 5，合计 $310。
	if !client.CostAvailable || math.Abs(client.CostUSD-310) > 0.000000001 {
		t.Fatalf("expected active pricing dimensions to aggregate to $310, got %+v", client)
	}
}

func TestListUsageClientsWithFilterAggregatesInSQLite(t *testing.T) {
	db, err := OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "usage-client-query-shape.db")})
	if err != nil {
		t.Fatalf("OpenDatabase returned error: %v", err)
	}
	closeTestDatabase(t, db)
	ip := "203.0.113.20"
	if _, _, err := InsertUsageEvents(db, []entities.UsageEvent{
		{EventKey: "query-shape-1", ClientIP: &ip, Timestamp: time.Now(), TotalTokens: 1},
		{EventKey: "query-shape-2", ClientIP: &ip, Timestamp: time.Now(), TotalTokens: 2},
	}); err != nil {
		t.Fatalf("InsertUsageEvents returned error: %v", err)
	}

	recorder := &usageClientsSQLRecorder{}
	queryLogger := gormlogger.New(recorder, gormlogger.Config{LogLevel: gormlogger.Info})
	queryDB := db.Session(&gorm.Session{Logger: queryLogger})
	page, err := ListUsageClientsWithFilter(queryDB, dto.UsageQueryFilter{Page: 1, PageSize: 20}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("ListUsageClientsWithFilter returned error: %v", err)
	}
	if len(page.Clients) != 1 || page.Clients[0].TotalTokens != 3 {
		t.Fatalf("unexpected aggregate result: %+v", page)
	}

	queryLog := strings.ToLower(recorder.logs.String())
	if !strings.Contains(queryLog, "group by") || !strings.Contains(queryLog, "sum(input_tokens)") || !strings.Contains(queryLog, "count(*)") {
		t.Fatalf("expected SQLite aggregate query instead of raw event loading, SQL logs:\n%s", recorder.logs.String())
	}
}

func TestListUsageClientsWithFilterSupportsIPUserAgentSearchSortAndPagination(t *testing.T) {
	db, err := OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "usage-client-views.db")})
	if err != nil {
		t.Fatalf("OpenDatabase returned error: %v", err)
	}
	closeTestDatabase(t, db)

	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	ipA, ipB := "192.0.2.1", "192.0.2.2"
	literalPercent, literalUnderscore, literalBackslash := "192.0.2.%", "192.0.2._", `192.0.2.\`
	uaA, uaB := "client/a", "client/b"
	events := []entities.UsageEvent{
		{EventKey: "a-1", ClientIP: &ipA, UserAgent: &uaA, Timestamp: now, TotalTokens: 10},
		{EventKey: "a-2", ClientIP: &ipA, UserAgent: &uaA, Timestamp: now.Add(time.Minute), Failed: true, TotalTokens: 20},
		{EventKey: "a-3", ClientIP: &ipA, UserAgent: &uaB, Timestamp: now.Add(2 * time.Minute), TotalTokens: 30},
		{EventKey: "b-1", ClientIP: &ipB, UserAgent: &uaA, Timestamp: now.Add(3 * time.Minute), TotalTokens: 40},
		{EventKey: "percent", ClientIP: &literalPercent, UserAgent: &uaA, Timestamp: now.Add(4 * time.Minute), TotalTokens: 50},
		{EventKey: "underscore", ClientIP: &literalUnderscore, UserAgent: &uaA, Timestamp: now.Add(5 * time.Minute), TotalTokens: 60},
		{EventKey: "backslash", ClientIP: &literalBackslash, UserAgent: &uaA, Timestamp: now.Add(6 * time.Minute), TotalTokens: 70},
	}
	if _, _, err := InsertUsageEvents(db, events); err != nil {
		t.Fatalf("InsertUsageEvents returned error: %v", err)
	}

	page, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{
		Page: 1, PageSize: 1, ClientGroupBy: "ip_user_agent",
		ClientSearch: "192.0.2.1", ClientSortBy: "request_count", ClientSortOrder: "desc",
	}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("ListUsageClientsWithFilter returned error: %v", err)
	}
	if page.TotalCount != 2 || page.TotalPages != 2 || len(page.Clients) != 1 {
		t.Fatalf("unexpected grouped page: %+v", page)
	}
	if got := page.Clients[0]; got.ClientIP != ipA || got.UserAgent != uaA || got.RequestCount != 2 || got.FailureCount != 1 {
		t.Fatalf("expected busiest IP+UA group first, got %+v", got)
	}

	literalPage, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{
		Page: 1, PageSize: 20, ClientGroupBy: "ip", ClientSearch: "%",
	}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("literal search returned error: %v", err)
	}
	if literalPage.TotalCount != 1 || len(literalPage.Clients) != 1 || literalPage.Clients[0].ClientIP != literalPercent {
		t.Fatalf("expected LIKE wildcard to be escaped, got %+v", literalPage)
	}
	for search, expected := range map[string]string{"_": literalUnderscore, `\`: literalBackslash} {
		literalPage, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{
			Page: 1, PageSize: 20, ClientGroupBy: "ip", ClientSearch: search,
		}, emptyPricingResolverForTest())
		if err != nil {
			t.Fatalf("literal search %q returned error: %v", search, err)
		}
		if literalPage.TotalCount != 1 || len(literalPage.Clients) != 1 || literalPage.Clients[0].ClientIP != expected {
			t.Fatalf("expected literal search %q to return %q, got %+v", search, expected, literalPage)
		}
	}

	overflowPage, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{
		Page: int(^uint(0) >> 1), PageSize: 20, ClientGroupBy: "ip",
	}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("large page returned error: %v", err)
	}
	if len(overflowPage.Clients) != 0 {
		t.Fatalf("expected an out-of-range large page to be empty, got %+v", overflowPage)
	}
}

func TestListUsageClientsWithFilterPropagatesUnavailableCost(t *testing.T) {
	db, err := OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "usage-client-cost.db")})
	if err != nil {
		t.Fatalf("OpenDatabase returned error: %v", err)
	}
	closeTestDatabase(t, db)
	ip := "203.0.113.10"
	if _, _, err := InsertUsageEvents(db, []entities.UsageEvent{{
		EventKey: "missing-price", ClientIP: &ip, Model: "missing-model", Timestamp: time.Now(), InputTokens: 1, TotalTokens: 1,
	}}); err != nil {
		t.Fatalf("InsertUsageEvents returned error: %v", err)
	}
	page, err := ListUsageClientsWithFilter(db, dto.UsageQueryFilter{Page: 1, PageSize: 20}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("ListUsageClientsWithFilter returned error: %v", err)
	}
	if len(page.Clients) != 1 || page.Clients[0].CostAvailable || page.Clients[0].CostUSD != 0 {
		t.Fatalf("expected missing price to make group cost unavailable, got %+v", page)
	}
}

func TestListUsageEventsWithFilterAppliesNormalizedClientIPFilter(t *testing.T) {
	db, err := OpenDatabase(config.Config{SQLitePath: filepath.Join(t.TempDir(), "usage-events-client-filter.db")})
	if err != nil {
		t.Fatalf("OpenDatabase returned error: %v", err)
	}
	closeTestDatabase(t, db)

	ipWithSpace, otherIP, blankIP := " 10.1.2.3 ", "10.1.2.4", "  "
	literalUnknown := "unknown"
	events := []entities.UsageEvent{
		{EventKey: "exact", ClientIP: &ipWithSpace, Timestamp: time.Now(), TotalTokens: 1},
		{EventKey: "other", ClientIP: &otherIP, Timestamp: time.Now(), TotalTokens: 1},
		{EventKey: "unknown-null", Timestamp: time.Now(), TotalTokens: 1},
		{EventKey: "unknown-blank", ClientIP: &blankIP, Timestamp: time.Now(), TotalTokens: 1},
		{EventKey: "unknown-literal", ClientIP: &literalUnknown, Timestamp: time.Now(), TotalTokens: 1},
	}
	if _, _, err := InsertUsageEvents(db, events); err != nil {
		t.Fatalf("InsertUsageEvents returned error: %v", err)
	}

	exact, err := ListUsageEventsWithFilter(db, dto.UsageQueryFilter{Page: 1, PageSize: 20, ClientIP: "10.1.2.3"}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("exact client filter returned error: %v", err)
	}
	if exact.TotalCount != 1 || len(exact.Events) != 1 {
		t.Fatalf("expected normalized exact IP match, got %+v", exact)
	}

	unknown, err := ListUsageEventsWithFilter(db, dto.UsageQueryFilter{Page: 1, PageSize: 20, ClientIP: "unknown"}, emptyPricingResolverForTest())
	if err != nil {
		t.Fatalf("unknown client filter returned error: %v", err)
	}
	if unknown.TotalCount != 3 || len(unknown.Events) != 3 {
		t.Fatalf("expected null, blank, and literal unknown IPs in unknown filter, got %+v", unknown)
	}
}
