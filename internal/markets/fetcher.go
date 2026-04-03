package markets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const graphqlQuery = `
query PriceArenaBaseData($kind: String!, $isActive: Boolean!) {
  PredictionConfig {
    chainId
    minBetUsd
    predictionBet
    predictionSettle
    updatedAt
  }
  PredictionMarket(where: { kind: { _eq: $kind }, isActive: { _eq: $isActive } }) {
    id
    name
    kind
    base
    marketHash
    ruleType
    assets
    periodKeys
    periods(order_by: { period: asc }) {
      period
      periodSeconds
      status
      winRatio
      openFeeP
      winCloseFeeP
      loseCloseFeeP
      payoutBps
      netWinPayoutBps
      maxUpUsd
      maxDownUsd
      maxSideUsd
    }
  }
}
`

type graphqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type graphqlResponse struct {
	Data struct {
		PredictionConfig []PredictionConfig `json:"PredictionConfig"`
		PredictionMarket []Market           `json:"PredictionMarket"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type Fetcher struct {
	hasuraURL   string
	adminSecret string
	httpClient  *http.Client
}

func NewFetcher(hasuraURL string, adminSecret string) *Fetcher {
	return &Fetcher{
		hasuraURL:   hasuraURL,
		adminSecret: adminSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (f *Fetcher) FetchMarkets(ctx context.Context, kind string, isActive bool) (*BaseData, error) {
	reqBody := graphqlRequest{
		Query: graphqlQuery,
		Variables: map[string]interface{}{
			"kind":     kind,
			"isActive": isActive,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", f.hasuraURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if f.adminSecret != "" {
		req.Header.Set("x-hasura-admin-secret", f.adminSecret)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch markets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	var gqlResp graphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	var config *PredictionConfig
	if len(gqlResp.Data.PredictionConfig) > 0 {
		config = &gqlResp.Data.PredictionConfig[0]
	}

	return &BaseData{
		Config:  config,
		Markets: gqlResp.Data.PredictionMarket,
	}, nil
}

func (f *Fetcher) FetchAllMarkets(ctx context.Context) (*BaseData, *BaseData, error) {
	upDown, err := f.FetchMarkets(ctx, MarketKindUpDown, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch UP_DOWN markets: %w", err)
	}

	relative, err := f.FetchMarkets(ctx, MarketKindRelative, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch RELATIVE markets: %w", err)
	}

	return upDown, relative, nil
}

// GetAvailablePeriods returns periods that are available for betting
func GetAvailablePeriods(market *Market) []Period {
	var available []Period
	for _, p := range market.Periods {
		if p.IsAvailable() {
			available = append(available, p)
		}
	}
	return available
}

// FilterByTimeframes filters periods to only include specified timeframes
func FilterByTimeframes(periods []Period, timeframes []string) []Period {
	tfSet := make(map[string]bool)
	for _, tf := range timeframes {
		if period, ok := TimeframeToPeriod[tf]; ok {
			tfSet[fmt.Sprintf("%d", period)] = true
		}
	}

	var filtered []Period
	for _, p := range periods {
		if tfSet[p.Period] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}
