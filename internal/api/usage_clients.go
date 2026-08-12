package api

import (
	"net/http"
	"time"

	"cpa-usage-keeper/internal/service"
	servicedto "cpa-usage-keeper/internal/service/dto"
	"cpa-usage-keeper/internal/timeutil"

	"github.com/gin-gonic/gin"
)

type usageClientsResponse struct {
	Clients    []usageClientPayload `json:"clients"`
	TotalCount int64                `json:"total_count"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"page_size"`
	TotalPages int                  `json:"total_pages"`
}

type usageClientPayload struct {
	ClientIP            string  `json:"client_ip"`
	UserAgent           string  `json:"user_agent,omitempty"`
	RequestCount        int64   `json:"request_count"`
	FailureCount        int64   `json:"failure_count"`
	FailureRate         float64 `json:"failure_rate"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	CostAvailable       bool    `json:"cost_available"`
	FirstSeenAt         string  `json:"first_seen_at"`
	LastSeenAt          string  `json:"last_seen_at"`
	PrimaryUserAgent    string  `json:"primary_user_agent"`
	UserAgentCount      int64   `json:"user_agent_count"`
}

func registerUsageClientsRoute(router gin.IRoutes, usageProvider service.UsageProvider) {
	clientProvider, _ := usageProvider.(service.UsageClientProvider)
	router.GET("/usage/clients", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if clientProvider == nil {
			c.JSON(http.StatusOK, usageClientsResponse{Clients: []usageClientPayload{}, Page: 1, PageSize: servicedto.DefaultUsageClientsLimit})
			return
		}
		filter, err := parseUsageClientsFilterQuery(c.Request, timeutil.NormalizeStorageTime(time.Now()))
		if err != nil {
			writeUsageFilterParseError(c, err)
			return
		}
		page, err := clientProvider.ListUsageClients(c.Request.Context(), filter)
		if err != nil {
			writeInternalError(c, "list usage clients failed", err)
			return
		}
		payload := make([]usageClientPayload, 0, len(page.Clients))
		for _, row := range page.Clients {
			payload = append(payload, usageClientPayload{
				ClientIP: row.ClientIP, UserAgent: row.UserAgent,
				RequestCount: row.RequestCount, FailureCount: row.FailureCount, FailureRate: row.FailureRate,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, ReasoningTokens: row.ReasoningTokens,
				CacheReadTokens: row.CacheReadTokens, CacheCreationTokens: row.CacheCreationTokens, TotalTokens: row.TotalTokens,
				CostUSD: row.CostUSD, CostAvailable: row.CostAvailable,
				FirstSeenAt: timeutil.FormatStorageTime(row.FirstSeenAt), LastSeenAt: timeutil.FormatStorageTime(row.LastSeenAt),
				PrimaryUserAgent: row.PrimaryUserAgent, UserAgentCount: row.UserAgentCount,
			})
		}
		c.JSON(http.StatusOK, usageClientsResponse{
			Clients: payload, TotalCount: page.TotalCount, Page: page.Page, PageSize: page.PageSize, TotalPages: page.TotalPages,
		})
	})
}
