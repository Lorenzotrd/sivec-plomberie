package betting

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
)

// BetType represents the type of bet
type BetType string

const (
	BetTypeUpDown   BetType = "UP_DOWN"
	BetTypeRelative BetType = "RELATIVE"
)

// BetSelection represents a selected bet configuration
type BetSelection struct {
	Type        BetType
	Market      *markets.Market
	Period      *markets.Period
	Direction   bool     // For UP_DOWN: true=UP, false=DOWN
	RuleType    uint8    // For RELATIVE: 0=highest, 1=lowest
	SideIndex   uint8    // For RELATIVE: index of picked asset
	AssetSymbol string   // Symbol of picked asset (e.g., "BTC")
	Amount      float64
	PriceIDs    []string // Price IDs needed for this bet
	AllSymbols  []string // All asset symbols in market (for RELATIVE display)
}

// Strategy handles random bet selection
type Strategy struct {
	config        config.BettingConfig
	assets        map[string]config.Asset
	assetSymbolMap map[string]string // address (lowercase) → symbol
	assetPriceIDs  map[string]string // symbol → Pyth price ID
}

// NewStrategy creates a new betting strategy.
// Asset address→symbol and symbol→priceID maps are built from config
// so new assets can be added without code changes.
func NewStrategy(cfg config.BettingConfig, assets map[string]config.Asset) *Strategy {
	symbolMap := make(map[string]string, len(assets))
	priceIDs := make(map[string]string, len(assets))
	for symbol, asset := range assets {
		symbolMap[strings.ToLower(asset.Address)] = strings.ToUpper(symbol)
		// Strip leading 0x from price ID if present
		pid := asset.PriceID
		pid = strings.TrimPrefix(pid, "0x")
		priceIDs[strings.ToUpper(symbol)] = pid
	}
	return &Strategy{
		config:        cfg,
		assets:        assets,
		assetSymbolMap: symbolMap,
		assetPriceIDs:  priceIDs,
	}
}

// SelectRandomBet selects a random bet from available markets
func (s *Strategy) SelectRandomBet(upDownMarkets, relativeMarkets []markets.Market) (*BetSelection, error) {
	// Filter markets to enabled assets and timeframes
	validUpDown := s.filterUpDownMarkets(upDownMarkets)
	validRelative := s.filterRelativeMarkets(relativeMarkets)

	if len(validUpDown) == 0 && len(validRelative) == 0 {
		return nil, fmt.Errorf("no valid markets available")
	}

	// Randomly select market type
	var betType BetType
	if len(validUpDown) == 0 {
		betType = BetTypeRelative
	} else if len(validRelative) == 0 {
		betType = BetTypeUpDown
	} else {
		if rand.Float64() < 0.5 {
			betType = BetTypeUpDown
		} else {
			betType = BetTypeRelative
		}
	}

	// Select based on type
	if betType == BetTypeUpDown {
		return s.selectUpDownBet(validUpDown)
	}
	return s.selectRelativeBet(validRelative)
}

func (s *Strategy) filterUpDownMarkets(mkts []markets.Market) []markets.Market {
	var valid []markets.Market
	enabledAssets := make(map[string]bool)
	for _, asset := range s.config.EnabledAssets {
		enabledAssets[strings.ToUpper(asset)] = true
	}

	for i := range mkts {
		m := &mkts[i]
		if m.Kind != markets.MarketKindUpDown || m.Base == nil {
			continue
		}

		// Check if asset is enabled
		symbol := s.assetSymbolMap[strings.ToLower(*m.Base)]
		if symbol == "" || !enabledAssets[symbol] {
			continue
		}

		// Check if any enabled timeframe is available
		periods := markets.GetAvailablePeriods(m)
		filtered := markets.FilterByTimeframes(periods, s.config.EnabledTimeframes)
		if len(filtered) > 0 {
			valid = append(valid, mkts[i])
		}
	}

	return valid
}

func (s *Strategy) filterRelativeMarkets(mkts []markets.Market) []markets.Market {
	var valid []markets.Market
	enabledAssets := make(map[string]bool)
	for _, asset := range s.config.EnabledAssets {
		enabledAssets[strings.ToUpper(asset)] = true
	}

	for i := range mkts {
		m := &mkts[i]
		if m.Kind != markets.MarketKindRelative {
			continue
		}

		// Check if all assets are enabled
		allEnabled := true
		for _, assetAddr := range m.Assets {
			symbol := s.assetSymbolMap[strings.ToLower(assetAddr)]
			if symbol == "" || !enabledAssets[symbol] {
				allEnabled = false
				break
			}
		}
		if !allEnabled {
			continue
		}

		// Check if any enabled timeframe is available
		periods := markets.GetAvailablePeriods(m)
		filtered := markets.FilterByTimeframes(periods, s.config.EnabledTimeframes)
		if len(filtered) > 0 {
			valid = append(valid, mkts[i])
		}
	}

	return valid
}

func (s *Strategy) selectUpDownBet(mkts []markets.Market) (*BetSelection, error) {
	if len(mkts) == 0 {
		return nil, fmt.Errorf("no UP_DOWN markets available")
	}

	// Random market
	market := &mkts[rand.Intn(len(mkts))]

	// Random available period
	periods := markets.GetAvailablePeriods(market)
	filtered := markets.FilterByTimeframes(periods, s.config.EnabledTimeframes)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no available periods for market")
	}
	period := &filtered[rand.Intn(len(filtered))]

	// Random direction
	isUp := rand.Float64() < 0.5

	// Random amount
	amount := s.randomAmount()

	// Get asset symbol and price ID
	symbol := s.assetSymbolMap[strings.ToLower(*market.Base)]
	priceID := s.assetPriceIDs[symbol]

	return &BetSelection{
		Type:        BetTypeUpDown,
		Market:      market,
		Period:      period,
		Direction:   isUp,
		AssetSymbol: symbol,
		Amount:      amount,
		PriceIDs:    []string{priceID},
	}, nil
}

func (s *Strategy) selectRelativeBet(mkts []markets.Market) (*BetSelection, error) {
	if len(mkts) == 0 {
		return nil, fmt.Errorf("no RELATIVE markets available")
	}

	// Random market
	market := &mkts[rand.Intn(len(mkts))]

	// Random available period
	periods := markets.GetAvailablePeriods(market)
	filtered := markets.FilterByTimeframes(periods, s.config.EnabledTimeframes)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no available periods for market")
	}
	period := &filtered[rand.Intn(len(filtered))]

	// Random rule type (highest=0 or lowest=1)
	ruleType := uint8(0)
	if rand.Float64() < 0.5 {
		ruleType = 1
	}

	// Random side index (picked asset)
	if len(market.Assets) == 0 {
		return nil, fmt.Errorf("market has no assets")
	}
	sideIndex := uint8(rand.Intn(len(market.Assets)))

	// Get asset symbol
	symbol := s.assetSymbolMap[strings.ToLower(market.Assets[sideIndex])]

	// Random amount
	amount := s.randomAmount()

	// Get all price IDs and symbols for the market
	var priceIDs []string
	var allSymbols []string
	for _, assetAddr := range market.Assets {
		sym := s.assetSymbolMap[strings.ToLower(assetAddr)]
		allSymbols = append(allSymbols, sym)
		if priceID, ok := s.assetPriceIDs[sym]; ok {
			priceIDs = append(priceIDs, priceID)
		}
	}

	return &BetSelection{
		Type:        BetTypeRelative,
		Market:      market,
		Period:      period,
		RuleType:    ruleType,
		SideIndex:   sideIndex,
		AssetSymbol: symbol,
		Amount:      amount,
		PriceIDs:    priceIDs,
		AllSymbols:  allSymbols,
	}, nil
}

func (s *Strategy) randomAmount() float64 {
	diff := s.config.MaxAmountUSDC - s.config.MinAmountUSDC
	return s.config.MinAmountUSDC + rand.Float64()*diff
}

// GetDirectionString returns "UP" or "DOWN" for display
func (b *BetSelection) GetDirectionString() string {
	if b.Type == BetTypeUpDown {
		if b.Direction {
			return "UP"
		}
		return "DOWN"
	}
	if b.RuleType == 0 {
		return "HIGHEST"
	}
	return "LOWEST"
}

// GetMarketDescription returns a human-readable market description
func (b *BetSelection) GetMarketDescription() string {
	if b.Type == BetTypeUpDown {
		return fmt.Sprintf("%s/USD", b.AssetSymbol)
	}
	// For relative, list all assets
	return strings.Join(b.AllSymbols, " vs ")
}

// CreateOppositeBet returns the opposite of the given bet
// e.g. UP -> DOWN, DOWN -> UP
func (s *Strategy) CreateOppositeBet(bet *BetSelection) *BetSelection {
	newBet := *bet

	if bet.Type == BetTypeUpDown {
		newBet.Direction = !bet.Direction
	} else if bet.Type == BetTypeRelative {
		// For relative, "opposite" is trickier.
		// If rule is "HIGHEST", opposite is "LOWEST"?
		// Or pick the other asset for the same rule?
		// User example: "btc up wallet 1 so wallet 2 btc down" - refers to UP/DOWN.
		// For relative, simply swapping RuleType (Highest <-> Lowest) is a valid opposite strategy.
		if bet.RuleType == 0 {
			newBet.RuleType = 1
		} else {
			newBet.RuleType = 0
		}
	}

	return &newBet
}
