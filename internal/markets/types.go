package markets

// PredictionConfig represents the prediction configuration from Hasura
type PredictionConfig struct {
	ChainID          interface{} `json:"chainId"`   // Can be string or int
	MinBetUsd        interface{} `json:"minBetUsd"` // Can be string or float
	PredictionBet    bool        `json:"predictionBet"`
	PredictionSettle bool        `json:"predictionSettle"`
	UpdatedAt        string      `json:"updatedAt"`
}

// Period represents a market period configuration
type Period struct {
	Period          string    `json:"period"`
	PeriodSeconds   string    `json:"periodSeconds"`
	Status          string    `json:"status"`
	WinRatio        *string   `json:"winRatio"`
	OpenFeeP        *string   `json:"openFeeP"`
	WinCloseFeeP    *string   `json:"winCloseFeeP"`
	LoseCloseFeeP   *string   `json:"loseCloseFeeP"`
	PayoutBps       *string   `json:"payoutBps"`
	NetWinPayoutBps *string   `json:"netWinPayoutBps"`
	MaxUpUsd        *string   `json:"maxUpUsd"`
	MaxDownUsd      *string   `json:"maxDownUsd"`
	MaxSideUsd      *[]string `json:"maxSideUsd"`
}

// Market represents a prediction market from Hasura
type Market struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // "UP_DOWN" or "RELATIVE"
	Base       *string  `json:"base"` // Asset address for UP_DOWN markets
	MarketHash *string  `json:"marketHash"`
	RuleType   *string  `json:"ruleType"` // "0" or "1" for relative markets
	Assets     []string `json:"assets"`
	PeriodKeys []string `json:"periodKeys"`
	Periods    []Period `json:"periods"`
}

// BaseData represents the combined config and markets data
type BaseData struct {
	Config  *PredictionConfig `json:"config"`
	Markets []Market          `json:"markets"`
}

// MarketKind constants
const (
	MarketKindUpDown   = "UP_DOWN"
	MarketKindRelative = "RELATIVE"
)

// Period status constants (as strings from Hasura)
const (
	PeriodStatusAvailable = "0"
	PeriodStatusCloseOnly = "1"
	PeriodStatusClosed    = "2"
)

// RuleType constants for relative markets
const (
	RuleTypeHighest = "0" // UP_LEADER - best performer
	RuleTypeLowest  = "1" // DOWN_LEADER - worst performer
)

// TimeframeToPeriod maps timeframe strings to period IDs
var TimeframeToPeriod = map[string]int{
	"1m":  0,
	"5m":  1,
	"10m": 2,
	"15m": 3,
	"30m": 4,
	"1h":  5,
}

// PeriodToTimeframe maps period IDs to timeframe strings
var PeriodToTimeframe = map[string]string{
	"0": "1m",
	"1": "5m",
	"2": "10m",
	"3": "15m",
	"4": "30m",
	"5": "1h",
}

// GetPeriodSeconds returns period duration in seconds based on period ID
func GetPeriodSeconds(periodStr string) int {
	switch periodStr {
	case "0":
		return 60
	case "1":
		return 300
	case "2":
		return 600
	case "3":
		return 900
	case "4":
		return 1800
	case "5":
		return 3600
	default:
		return 0
	}
}

// IsAvailable checks if a period is available for betting
func (p *Period) IsAvailable() bool {
	return p.Status == PeriodStatusAvailable
}
