package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/viper"
)

type Config struct {
	OwnerPrivateKey string           `mapstructure:"owner_private_key"`
	RPCUrl          string           `mapstructure:"rpc_url"`
	ChainID         int64            `mapstructure:"chain_id"`
	DiamondAddress  string           `mapstructure:"diamond_address"`
	USDCAddress     string           `mapstructure:"usdc_address"`
	HasuraURL       string           `mapstructure:"hasura_url"`
	HermesURL       string           `mapstructure:"hermes_url"`
	Wallets         WalletConfig     `mapstructure:"wallets"`
	Betting         BettingConfig    `mapstructure:"betting"`
	CandleRush      CandleRushConfig `mapstructure:"candle_rush"`
	Manager         ManagerConfig    `mapstructure:"manager"`
	Assets          map[string]Asset `mapstructure:"assets"`
	LogLevel        string           `mapstructure:"log_level"`
	UseStreaming    bool             `mapstructure:"use_streaming"`
	Threads         int              `mapstructure:"threads"` // Default 1
	ApiBaseURL      string           `mapstructure:"api_base_url"`
}

// ManagerConfig holds configuration for the unified bot manager
type ManagerConfig struct {
	EnabledBots        []string `mapstructure:"enabled_bots"`         // ["bet-bot", "candle-rush-bot"]
	SweepIntervalHours int      `mapstructure:"sweep_interval_hours"` // How often to sweep sub-wallet funds to owner (default 8)
}

// CandleRushConfig holds configuration for the Candle Rush bot
type CandleRushConfig struct {
	MultiAsset    bool     `mapstructure:"multi_asset"`
	Assets        []string `mapstructure:"assets"`
	SingleAsset   string   `mapstructure:"single_asset"` // Asset to use when multi_asset is false
	MinAmountUSDC float64  `mapstructure:"min_amount_usdc"`
	MaxAmountUSDC float64  `mapstructure:"max_amount_usdc"`
	Intervals     []int    `mapstructure:"intervals"`
	CooldownMin   int      `mapstructure:"cooldown_min"`
	CooldownMax   int      `mapstructure:"cooldown_max"`
	BrokerID      int      `mapstructure:"broker_id"`

	// Number of consecutive candles to bet on per interval per asset in each round
	CandlesPerInterval int `mapstructure:"candles_per_interval"`

	// Coverage-based halt time: halt = candlesPerInterval × intervalSeconds × (visible - target) / target
	VisibleCandles int `mapstructure:"visible_candles"` // Number of candles visible in UI (default: 14)
	TargetCovered  int `mapstructure:"target_covered"`  // Target candles with volume (default: 8)
}

type WalletConfig struct {
	DBUrl                 string  `mapstructure:"db_url"`
	NewWalletProbability  float64 `mapstructure:"new_wallet_probability"`
	MinFundAmountUSDC     float64 `mapstructure:"min_fund_amount_usdc"`
	MaxFundAmountUSDC     float64 `mapstructure:"max_fund_amount_usdc"`
	MinGasFundMON         float64 `mapstructure:"min_gas_fund_mon"`
	MaxGasFundMON         float64 `mapstructure:"max_gas_fund_mon"`
	MinGasBalanceMON      float64 `mapstructure:"min_gas_balance_mon"`
	MinWalletsBeforeReuse int     `mapstructure:"min_wallets_before_reuse"`
	SingleWalletMode      bool    `mapstructure:"single_wallet_mode"`
	GenerateNewWallets    *bool   `mapstructure:"generate_new_wallets"` // nil = true (default), false = only use existing wallets
	Mnemonic              string  `mapstructure:"mnemonic"`
	InitialWalletCount    int     `mapstructure:"initial_wallet_count"`
	ReferralCode          string  `mapstructure:"referral_code"`
}

// ShouldGenerateNewWallets returns whether on-the-fly wallet generation is enabled.
// Defaults to true if not explicitly set.
func (w *WalletConfig) ShouldGenerateNewWallets() bool {
	if w.GenerateNewWallets == nil {
		return true
	}
	return *w.GenerateNewWallets
}

type BettingConfig struct {
	MinAmountUSDC      float64  `mapstructure:"min_amount_usdc"`
	MaxAmountUSDC      float64  `mapstructure:"max_amount_usdc"`
	CooldownSeconds    int      `mapstructure:"cooldown_seconds"`
	ToleranceBps       int      `mapstructure:"tolerance_bps"`
	MaxPriceAgeSeconds int      `mapstructure:"max_price_age_seconds"`
	PriceRetries       int      `mapstructure:"price_retries"`
	BrokerID           int      `mapstructure:"broker_id"`
	EnabledAssets      []string `mapstructure:"enabled_assets"`
	EnabledTimeframes  []string `mapstructure:"enabled_timeframes"`
}

type Asset struct {
	Address string `mapstructure:"address"`
	PriceID string `mapstructure:"price_id"`
}

// MaskRPCUrl returns the RPC URL with path and query parameters masked
// to prevent API key leakage in logs.
func (c *Config) MaskRPCUrl() string {
	url := c.RPCUrl
	// Find the end of the host portion (after ://)
	schemeEnd := strings.Index(url, "://")
	if schemeEnd == -1 {
		return url
	}
	rest := url[schemeEnd+3:]
	// Find the first / after the host
	pathStart := strings.Index(rest, "/")
	if pathStart == -1 {
		return url // No path, safe to show
	}
	host := rest[:pathStart]
	return url[:schemeEnd+3] + host + "/***"
}

func (c *Config) GetCooldown() time.Duration {
	return time.Duration(c.Betting.CooldownSeconds) * time.Second
}

// GetMaxPriceAge returns the maximum age for price data (default 10s if not set)
func (c *Config) GetMaxPriceAge() time.Duration {
	if c.Betting.MaxPriceAgeSeconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(c.Betting.MaxPriceAgeSeconds) * time.Second
}

// GetPriceRetries returns the number of retries for fetching fresh prices (default 3 if not set)
func (c *Config) GetPriceRetries() int {
	if c.Betting.PriceRetries <= 0 {
		return 3
	}
	return c.Betting.PriceRetries
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Allow environment variable overrides
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Expand environment variables in string fields
	cfg.OwnerPrivateKey = expandEnv(cfg.OwnerPrivateKey)
	cfg.HasuraURL = expandEnv(cfg.HasuraURL)
	cfg.HermesURL = expandEnv(cfg.HermesURL)
	cfg.RPCUrl = expandEnv(cfg.RPCUrl)
	cfg.Wallets.Mnemonic = expandEnv(cfg.Wallets.Mnemonic)
	cfg.Wallets.DBUrl = expandEnv(cfg.Wallets.DBUrl)
	cfg.ApiBaseURL = expandEnv(cfg.ApiBaseURL)

	// Apply common env-var overrides
	applyEnvOverrides(&cfg)

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyEnvOverrides applies common environment variable overrides to the config.
// Called from both Load() and LoadManager() to avoid duplication.
func applyEnvOverrides(cfg *Config) {
	if singleWalletMode := os.Getenv("SINGLE_WALLET_MODE"); singleWalletMode != "" {
		cfg.Wallets.SingleWalletMode = singleWalletMode == "true"
	}
	if genWallets := os.Getenv("GENERATE_NEW_WALLETS"); genWallets != "" {
		val := genWallets == "true"
		cfg.Wallets.GenerateNewWallets = &val
	}
	if mnemonic := os.Getenv("MNEMONIC"); mnemonic != "" {
		cfg.Wallets.Mnemonic = mnemonic
	}
	if initialCountStr := os.Getenv("INITIAL_WALLET_COUNT"); initialCountStr != "" {
		if count, err := strconv.Atoi(initialCountStr); err == nil {
			cfg.Wallets.InitialWalletCount = count
		}
	}
	if referralCode := os.Getenv("REFERRAL_CODE"); referralCode != "" {
		cfg.Wallets.ReferralCode = referralCode
	}
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		cfg.Wallets.DBUrl = dbURL
	}
	if apiBaseURL := os.Getenv("API_BASE_URL"); apiBaseURL != "" {
		cfg.ApiBaseURL = apiBaseURL
	}
	if hermesURL := os.Getenv("HERMES_URL"); hermesURL != "" {
		cfg.HermesURL = hermesURL
	}
	if threadsStr := os.Getenv("THREADS"); threadsStr != "" {
		if threads, err := strconv.Atoi(threadsStr); err == nil {
			cfg.Threads = threads
		}
	}
	if cooldownStr := os.Getenv("COOLDOWN_SECONDS"); cooldownStr != "" {
		if cooldown, err := strconv.Atoi(cooldownStr); err == nil {
			cfg.Betting.CooldownSeconds = cooldown
		}
	}
}

func expandEnv(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		envVar := s[2 : len(s)-1]
		return os.Getenv(envVar)
	}
	return s
}

func (c *Config) Validate() error {
	if c.OwnerPrivateKey == "" {
		return fmt.Errorf("owner_private_key is required (set OWNER_PRIVATE_KEY env var)")
	}
	if c.RPCUrl == "" {
		return fmt.Errorf("rpc_url is required")
	}
	if err := validateAddress("diamond_address", c.DiamondAddress); err != nil {
		return err
	}
	if err := validateAddress("usdc_address", c.USDCAddress); err != nil {
		return err
	}
	if c.HasuraURL == "" {
		return fmt.Errorf("hasura_url is required (set HASURA_URL env var)")
	}
	if len(c.Betting.EnabledAssets) == 0 {
		return fmt.Errorf("at least one enabled asset is required")
	}
	if len(c.Betting.EnabledTimeframes) == 0 {
		return fmt.Errorf("at least one enabled timeframe is required")
	}
	return nil
}

// validateAddress checks that a config field contains a valid Ethereum address.
func validateAddress(fieldName, addr string) error {
	if addr == "" {
		return fmt.Errorf("%s is required", fieldName)
	}
	if !common.IsHexAddress(addr) {
		return fmt.Errorf("%s is not a valid Ethereum address: %s", fieldName, addr)
	}
	return nil
}

// LoadCandleRush loads config specifically for the candle-rush-bot
// It has relaxed validation since hasura_url and betting config are not needed
func LoadCandleRush(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Allow environment variable overrides
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Expand environment variables in string fields
	cfg.OwnerPrivateKey = expandEnv(cfg.OwnerPrivateKey)
	cfg.HermesURL = expandEnv(cfg.HermesURL)
	cfg.RPCUrl = expandEnv(cfg.RPCUrl)
	cfg.Wallets.DBUrl = expandEnv(cfg.Wallets.DBUrl)

	// Apply common env-var overrides
	applyEnvOverrides(&cfg)

	// Validate only what candle-rush-bot needs
	if err := cfg.ValidateCandleRush(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ValidateCandleRush validates only the fields required for candle-rush-bot
func (c *Config) ValidateCandleRush() error {
	if c.RPCUrl == "" {
		return fmt.Errorf("rpc_url is required")
	}
	if err := validateAddress("diamond_address", c.DiamondAddress); err != nil {
		return err
	}
	if err := validateAddress("usdc_address", c.USDCAddress); err != nil {
		return err
	}
	return nil
}

// LoadManager loads config for the unified bot manager.
// It skips owner_private_key validation (resolved via secrets)
// but requires base network config. It applies env var overrides
// similar to Load.
func LoadManager(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Expand environment variables in string fields
	cfg.OwnerPrivateKey = expandEnv(cfg.OwnerPrivateKey)
	cfg.HasuraURL = expandEnv(cfg.HasuraURL)
	cfg.HermesURL = expandEnv(cfg.HermesURL)
	cfg.RPCUrl = expandEnv(cfg.RPCUrl)
	cfg.Wallets.Mnemonic = expandEnv(cfg.Wallets.Mnemonic)
	cfg.Wallets.DBUrl = expandEnv(cfg.Wallets.DBUrl)
	cfg.ApiBaseURL = expandEnv(cfg.ApiBaseURL)

	// Apply common env-var overrides
	applyEnvOverrides(&cfg)

	// Default manager config: both bots enabled
	if len(cfg.Manager.EnabledBots) == 0 {
		cfg.Manager.EnabledBots = []string{"bet-bot", "candle-rush-bot"}
	}

	// Validate base network config (skip owner_private_key — resolved via secrets)
	if cfg.RPCUrl == "" {
		return nil, fmt.Errorf("rpc_url is required")
	}
	if err := validateAddress("diamond_address", cfg.DiamondAddress); err != nil {
		return nil, err
	}
	if err := validateAddress("usdc_address", cfg.USDCAddress); err != nil {
		return nil, err
	}

	return &cfg, nil
}
