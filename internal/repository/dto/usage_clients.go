package dto

import "time"

// UsageClientsPageRecord 是按客户端/IP 聚合后的分页结果。
type UsageClientsPageRecord struct {
	Clients    []UsageClientRecord
	TotalCount int64
	Page       int
	PageSize   int
	TotalPages int
}

// UsageClientRecord 是单个客户端/IP（或 IP + User-Agent）分组的用量摘要。
type UsageClientRecord struct {
	ClientIP            string
	UserAgent           string
	RequestCount        int64
	FailureCount        int64
	FailureRate         float64
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	TotalTokens         int64
	CostUSD             float64
	CostAvailable       bool
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
	PrimaryUserAgent    string
	UserAgentCount      int64
}
