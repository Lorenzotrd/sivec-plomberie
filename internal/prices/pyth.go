package prices

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Stream constants
	streamReconnectDelay = 5 * time.Second
	streamCacheTTL       = 30 * time.Second
	streamMaxAge         = 5 * time.Second

	// Price freshness threshold - reject prices older than this
	DefaultMaxPriceAge = 10 * time.Second
)

// ErrStalePrices is returned when fetched prices are too old
type ErrStalePrices struct {
	PriceID     string
	PublishTime time.Time
	Age         time.Duration
	MaxAge      time.Duration
}

func (e *ErrStalePrices) Error() string {
	return fmt.Sprintf("stale price for %s: published at %s (age: %s, max: %s)",
		e.PriceID, e.PublishTime.Format(time.RFC3339), e.Age.Round(time.Millisecond), e.MaxAge)
}

type PythPrice struct {
	Price       string `json:"price"`
	Conf        string `json:"conf"`
	Expo        int    `json:"expo"`
	PublishTime int64  `json:"publish_time"`
}

type PythPriceData struct {
	ID       string    `json:"id"`
	Price    PythPrice `json:"price"`
	EmaPrice PythPrice `json:"ema_price"`
}

type HermesResponse struct {
	Parsed []PythPriceData `json:"parsed"`
	Binary *HermesBinary   `json:"binary,omitempty"`
}

type HermesBinary struct {
	Data []string `json:"data"`
}

type StreamManager struct {
	streamURL     string
	cache         *StreamCache
	subMu         sync.RWMutex
	subscriptions map[string]bool
	httpClient    *http.Client
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

type StreamCache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	ttl     time.Duration
}

type CacheEntry struct {
	data      *PythPriceData
	timestamp time.Time
}

type Fetcher struct {
	hermesURL  string
	httpClient *http.Client
	streamer   *StreamManager
}

func NewFetcher(hermesURL string) *Fetcher {
	return &Fetcher{
		hermesURL: hermesURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewFetcherWithStreaming creates a fetcher with streaming support
func NewFetcherWithStreaming(hermesURL string, priceIDs []string) *Fetcher {
	streamer := NewStreamer(hermesURL, priceIDs)
	return &Fetcher{
		hermesURL:  hermesURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		streamer:   streamer,
	}
}

// FetchPrices fetches current prices for the given price IDs from Hermes
// Uses streaming cache if available, falls back to HTTP
func (f *Fetcher) FetchPrices(ctx context.Context, priceIDs []string) (map[string]*PythPriceData, error) {
	// If we have a streamer, try it first
	if f.streamer != nil {
		return f.streamer.FetchPrices(ctx, priceIDs)
	}
	if len(priceIDs) == 0 {
		return make(map[string]*PythPriceData), nil
	}

	// Build URL with query parameters
	u, err := url.Parse(f.hermesURL)
	if err != nil {
		return nil, fmt.Errorf("invalid hermes URL: %w", err)
	}

	q := u.Query()
	for _, id := range priceIDs {
		q.Add("ids[]", id)
	}
	q.Set("encoding", "hex")
	q.Set("parsed", "true")
	q.Set("ignore_invalid_price_ids", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch prices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	var hermesResp HermesResponse
	if err := json.NewDecoder(resp.Body).Decode(&hermesResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	result := make(map[string]*PythPriceData)
	for i := range hermesResp.Parsed {
		data := &hermesResp.Parsed[i]
		// Normalize ID (remove 0x prefix if present)
		id := data.ID
		if len(id) > 2 && id[:2] == "0x" {
			id = id[2:]
		}
		result[id] = data
	}

	return result, nil
}

// ConvertPythPriceTo1e8 converts a Pyth price to 1e8 format (uint64)
// This is the format expected by the smart contract
func ConvertPythPriceTo1e8(priceStr string, expo int) (uint64, error) {
	price, ok := new(big.Int).SetString(priceStr, 10)
	if !ok {
		return 0, fmt.Errorf("invalid price string: %s", priceStr)
	}

	// Target exponent is -8 (1e8 format)
	targetExpo := -8
	expoDiff := expo - targetExpo

	if expoDiff > 0 {
		// Need to divide (price is too large)
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(expoDiff)), nil)
		price = new(big.Int).Div(price, divisor)
	} else if expoDiff < 0 {
		// Need to multiply (price is too small)
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-expoDiff)), nil)
		price = new(big.Int).Mul(price, multiplier)
	}

	if !price.IsUint64() {
		return 0, fmt.Errorf("price overflow: %s", price.String())
	}

	return price.Uint64(), nil
}

// NewStreamer creates a new streaming price fetcher
func NewStreamer(hermesURL string, priceIDs []string) *StreamManager {
	ctx, cancel := context.WithCancel(context.Background())
	streamURL := strings.Replace(hermesURL, "/v2/updates/price/latest", "/v2/updates/price/stream", 1)

	manager := &StreamManager{
		streamURL:     streamURL,
		cache:         NewStreamCache(streamCacheTTL),
		subscriptions: make(map[string]bool),
		httpClient:    &http.Client{},
		ctx:           ctx,
		cancel:        cancel,
	}

	// Subscribe to initial price IDs
	for _, id := range priceIDs {
		manager.subscriptions[id] = true
	}

	// Start streaming in background
	manager.wg.Add(1)
	go manager.runStream()

	return manager
}

// runStream maintains the WebSocket/stream connection
func (sm *StreamManager) runStream() {
	defer sm.wg.Done()

	for {
		select {
		case <-sm.ctx.Done():
			return
		default:
			if err := sm.connectAndStream(); err != nil {
				fmt.Printf("Stream error: %v, reconnecting...\n", err)
				time.Sleep(streamReconnectDelay)
			}
		}
	}
}

// connectAndStream establishes connection and processes stream
func (sm *StreamManager) connectAndStream() error {
	// Build stream URL with subscribed price IDs
	u, err := url.Parse(sm.streamURL)
	if err != nil {
		return fmt.Errorf("invalid stream URL: %w", err)
	}

	q := u.Query()
	sm.subMu.RLock()
	for id := range sm.subscriptions {
		q.Add("ids[]", id)
	}
	sm.subMu.RUnlock()
	q.Set("parsed", "true")
	q.Set("encoding", "hex")
	q.Set("ignore_invalid_price_ids", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(sm.ctx, "GET", u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create stream request: %w", err)
	}

	resp, err := sm.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stream connection failed with status: %d", resp.StatusCode)
	}

	// Process Server-Sent Events
	scanner := bufio.NewScanner(resp.Body)
	var eventBuffer strings.Builder

	for scanner.Scan() {
		select {
		case <-sm.ctx.Done():
			return nil
		default:
			line := scanner.Text()

			if line == "" {
				// End of event
				if eventBuffer.Len() > 0 {
					sm.processSSEEvent(eventBuffer.String())
					eventBuffer.Reset()
				}
			} else if strings.HasPrefix(line, "data: ") {
				// Data line
				data := strings.TrimPrefix(line, "data: ")
				if eventBuffer.Len() > 0 {
					eventBuffer.WriteString("\n")
				}
				eventBuffer.WriteString(data)
			}
		}
	}

	return scanner.Err()
}

// processSSEEvent processes a Server-Sent Event
func (sm *StreamManager) processSSEEvent(eventData string) {
	var hermesResp HermesResponse
	if err := json.Unmarshal([]byte(eventData), &hermesResp); err != nil {
		fmt.Printf("Failed to parse stream event: %v\n", err)
		return
	}

	// Update cache with new prices
	for i := range hermesResp.Parsed {
		data := &hermesResp.Parsed[i]
		// Normalize ID (remove 0x prefix if present)
		id := data.ID
		if len(id) > 2 && id[:2] == "0x" {
			id = id[2:]
		}
		sm.cache.Put(id, data)
	}
}

// FetchPrices fetches prices with stream fallback
func (sm *StreamManager) FetchPrices(ctx context.Context, priceIDs []string) (map[string]*PythPriceData, error) {
	result := make(map[string]*PythPriceData)

	// Try to get from cache first
	for _, id := range priceIDs {
		if entry := sm.cache.Get(id); entry != nil && time.Since(entry.timestamp) < streamMaxAge {
			result[id] = entry.data
		}
	}

	// If we have all prices from cache, return
	if len(result) == len(priceIDs) {
		return result, nil
	}

	// Fallback to HTTP fetch for missing prices
	missingIDs := make([]string, 0)
	for _, id := range priceIDs {
		if _, exists := result[id]; !exists {
			missingIDs = append(missingIDs, id)
		}
	}

	if len(missingIDs) > 0 {
		httpFetcher := NewFetcher(strings.Replace(sm.streamURL, "/stream", "/latest", 1))
		httpResult, err := httpFetcher.FetchPrices(ctx, missingIDs)
		if err != nil {
			return nil, fmt.Errorf("stream cache miss and HTTP fallback failed: %w", err)
		}

		// Merge results and update cache
		for id, data := range httpResult {
			result[id] = data
			sm.cache.Put(id, data)
		}
	}

	return result, nil
}

// Subscribe adds new price IDs to the stream subscription
func (sm *StreamManager) Subscribe(priceIDs []string) {
	sm.subMu.Lock()
	defer sm.subMu.Unlock()
	for _, id := range priceIDs {
		sm.subscriptions[id] = true
	}
	// Note: In a real implementation, you'd signal the stream to resubscribe
}

// Close shuts down the stream manager
func (sm *StreamManager) Close() {
	sm.cancel()
	sm.wg.Wait()
}

// NewStreamCache creates a new cache for stream data
func NewStreamCache(ttl time.Duration) *StreamCache {
	return &StreamCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
	}
}

// Put stores a price in the cache
func (sc *StreamCache) Put(id string, data *PythPriceData) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.entries[id] = &CacheEntry{
		data:      data,
		timestamp: time.Now(),
	}

	// Clean up expired entries
	sc.cleanup()
}

// Get retrieves a price from the cache
func (sc *StreamCache) Get(id string) *CacheEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	return sc.entries[id]
}

// cleanup removes expired entries
func (sc *StreamCache) cleanup() {
	now := time.Now()
	for id, entry := range sc.entries {
		if now.Sub(entry.timestamp) > sc.ttl {
			delete(sc.entries, id)
		}
	}
}

// CalculateAcceptablePrice calculates the acceptable entry price with tolerance
// For UP predictions (ruleType=0): allows higher price (worse for user)
// For DOWN predictions (ruleType=1): allows lower price (worse for user)
func CalculateAcceptablePrice(currentPrice uint64, toleranceBps int, isUp bool) uint64 {
	tolerance := float64(toleranceBps) / 10000.0
	priceFloat := float64(currentPrice)

	if isUp {
		// For UP, allow slightly higher price
		return uint64(math.Ceil(priceFloat * (1 + tolerance)))
	}
	// For DOWN, allow slightly lower price
	return uint64(math.Floor(priceFloat * (1 - tolerance)))
}

// CalculateAcceptableEntryPrices calculates acceptable prices for all assets in a relative market
// ruleType: 0 = HIGHEST (UP_LEADER), 1 = LOWEST (DOWN_LEADER)
func CalculateAcceptableEntryPrices(currentPrices []uint64, toleranceBps int, ruleType uint8) []uint64 {
	result := make([]uint64, len(currentPrices))
	// For HIGHEST (ruleType=0): allow higher prices (price can go up)
	// For LOWEST (ruleType=1): allow lower prices (price can go down)
	isUp := ruleType == 0
	for i, price := range currentPrices {
		result[i] = CalculateAcceptablePrice(price, toleranceBps, isUp)
	}
	return result
}

// Close shuts down any streaming connections
func (f *Fetcher) Close() {
	if f.streamer != nil {
		f.streamer.Close()
	}
}

// GetPriceAsFloat converts a 1e8 price to float64 for display
func GetPriceAsFloat(price1e8 uint64) float64 {
	return float64(price1e8) / 1e8
}

// GetPriceAge returns how old a price is based on its publish_time
func GetPriceAge(data *PythPriceData) time.Duration {
	publishTime := time.Unix(data.Price.PublishTime, 0)
	return time.Since(publishTime)
}

// ValidatePriceFreshness checks if all prices are within the max age threshold
// Returns ErrStalePrices if any price is too old
func ValidatePriceFreshness(priceData map[string]*PythPriceData, maxAge time.Duration) error {
	for id, data := range priceData {
		age := GetPriceAge(data)
		if age > maxAge {
			return &ErrStalePrices{
				PriceID:     id,
				PublishTime: time.Unix(data.Price.PublishTime, 0),
				Age:         age,
				MaxAge:      maxAge,
			}
		}
	}
	return nil
}

// FetchFreshPrices fetches prices and validates they are fresh
// It will retry up to maxRetries times if prices are stale
func (f *Fetcher) FetchFreshPrices(ctx context.Context, priceIDs []string, maxAge time.Duration, maxRetries int) (map[string]*PythPriceData, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Brief delay between retries to allow for new price updates
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}

		priceData, err := f.FetchPrices(ctx, priceIDs)
		if err != nil {
			lastErr = err
			continue
		}

		// Validate freshness
		if err := ValidatePriceFreshness(priceData, maxAge); err != nil {
			lastErr = err
			continue
		}

		// Prices are fresh!
		return priceData, nil
	}

	return nil, fmt.Errorf("failed to get fresh prices after %d attempts: %w", maxRetries+1, lastErr)
}
