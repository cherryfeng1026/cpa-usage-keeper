package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/auth"
	servicedto "cpa-usage-keeper/internal/service/dto"
)

type usageClientsStub struct {
	*usageEventsStub
	clientsPage  *servicedto.UsageClientsPage
	clientsErr   error
	clientCalls  int
	clientFilter servicedto.UsageFilter
}

func (s *usageClientsStub) ListUsageClients(_ context.Context, filter servicedto.UsageFilter) (*servicedto.UsageClientsPage, error) {
	s.clientCalls++
	s.clientFilter = filter
	return s.clientsPage, s.clientsErr
}

func TestUsageClientsEndpointParsesFiltersAndReturnsAggregatePayload(t *testing.T) {
	firstSeen := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	lastSeen := firstSeen.Add(2 * time.Hour)
	provider := &usageClientsStub{
		usageEventsStub: &usageEventsStub{},
		clientsPage: &servicedto.UsageClientsPage{
			Clients: []servicedto.UsageClientRecord{{
				ClientIP: "10.0.0.1", UserAgent: "client/a", RequestCount: 4, FailureCount: 1, FailureRate: 25,
				InputTokens: 10, OutputTokens: 20, ReasoningTokens: 3, CacheReadTokens: 4, CacheCreationTokens: 5, TotalTokens: 42,
				CostUSD: 0.25, CostAvailable: true, FirstSeenAt: firstSeen, LastSeenAt: lastSeen,
				PrimaryUserAgent: "client/a", UserAgentCount: 2,
			}},
			TotalCount: 3, Page: 2, PageSize: 20, TotalPages: 2,
		},
	}
	router := NewRouter(nil, nil, provider, nil, AuthConfig{}, nil, "")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage/clients?range=24h&page=2&page_size=20&group_by=ip_user_agent&search=10.0&sort_by=request_count&sort_order=asc&api_key_id=7", nil)

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	assertNoStoreHeaders(t, response)
	if provider.clientCalls != 1 {
		t.Fatalf("expected one client provider call, got %d", provider.clientCalls)
	}
	filter := provider.clientFilter
	if filter.Page != 2 || filter.PageSize != 20 || filter.ClientGroupBy != "ip_user_agent" || filter.ClientSearch != "10.0" || filter.ClientSortBy != "request_count" || filter.ClientSortOrder != "asc" || filter.APIKeyID != "7" {
		t.Fatalf("unexpected clients filter: %+v", filter)
	}

	var payload struct {
		Clients    []map[string]any `json:"clients"`
		TotalCount int64            `json:"total_count"`
		Page       int              `json:"page"`
		PageSize   int              `json:"page_size"`
		TotalPages int              `json:"total_pages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.TotalCount != 3 || payload.Page != 2 || payload.PageSize != 20 || payload.TotalPages != 2 || len(payload.Clients) != 1 {
		t.Fatalf("unexpected response metadata: %+v", payload)
	}
	client := payload.Clients[0]
	for _, key := range []string{
		"client_ip", "user_agent", "request_count", "failure_count", "failure_rate",
		"input_tokens", "output_tokens", "reasoning_tokens", "cache_read_tokens", "cache_creation_tokens", "total_tokens",
		"cost_usd", "cost_available", "first_seen_at", "last_seen_at", "primary_user_agent", "user_agent_count",
	} {
		if _, ok := client[key]; !ok {
			t.Fatalf("expected client payload key %q, body=%s", key, response.Body.String())
		}
	}
}

func TestUsageClientsEndpointRejectsInvalidQueryParameters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "page", query: "page=0"},
		{name: "page size", query: "page_size=17"},
		{name: "group", query: "group_by=device"},
		{name: "sort", query: "sort_by=secret"},
		{name: "order", query: "sort_order=sideways"},
		{name: "long search", query: "search=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &usageClientsStub{usageEventsStub: &usageEventsStub{}, clientsPage: &servicedto.UsageClientsPage{}}
			router := NewRouter(nil, nil, provider, nil, AuthConfig{}, nil, "")
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/usage/clients?range=24h&"+tc.query, nil)

			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", response.Code, response.Body.String())
			}
			if provider.clientCalls != 0 {
				t.Fatalf("invalid query must not call provider, got %d calls", provider.clientCalls)
			}
		})
	}
}

func TestUsageClientsEndpointIsAdminOnly(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	adminToken, _, err := sessions.Create()
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	viewerToken, _, err := sessions.CreateAPIKeyViewer(42)
	if err != nil {
		t.Fatalf("create API Key viewer session: %v", err)
	}
	provider := &usageClientsStub{
		usageEventsStub: &usageEventsStub{},
		clientsPage:     &servicedto.UsageClientsPage{Clients: []servicedto.UsageClientRecord{}},
	}
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	router := NewRouter(nil, nil, provider, nil, config, NewAuthHandler(config, sessions), "")

	viewerResponse := httptest.NewRecorder()
	viewerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/usage/clients?range=24h", nil)
	viewerRequest.AddCookie(&http.Cookie{Name: "cpa_usage_keeper_session", Value: viewerToken})
	router.ServeHTTP(viewerResponse, viewerRequest)
	if viewerResponse.Code != http.StatusForbidden || provider.clientCalls != 0 {
		t.Fatalf("API Key viewer reached client usage: status=%d body=%s calls=%d", viewerResponse.Code, viewerResponse.Body.String(), provider.clientCalls)
	}

	adminResponse := httptest.NewRecorder()
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/v1/usage/clients?range=24h", nil)
	adminRequest.AddCookie(&http.Cookie{Name: "cpa_usage_keeper_session", Value: adminToken})
	router.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || provider.clientCalls != 1 {
		t.Fatalf("admin could not reach client usage: status=%d body=%s calls=%d", adminResponse.Code, adminResponse.Body.String(), provider.clientCalls)
	}
}

func TestUsageEventsEndpointForwardsExactClientIPFilter(t *testing.T) {
	provider := &usageEventsStub{}
	router := NewRouter(nil, nil, provider, nil, AuthConfig{}, nil, "")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage/events?range=24h&client_ip=%20unknown%20", nil)

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	if provider.filterCalls != 1 || provider.lastFilter.ClientIP != "unknown" {
		t.Fatalf("expected normalized client_ip filter, calls=%d filter=%+v", provider.filterCalls, provider.lastFilter)
	}
}
