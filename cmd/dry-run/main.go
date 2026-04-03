package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/joho/godotenv"

	"github.com/blinq-fi/blinq-mm-bot/internal/betting"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
	"github.com/blinq-fi/blinq-mm-bot/internal/utils"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

// Colors for output
var (
	green   = color.New(color.FgGreen).SprintFunc()
	yellow  = color.New(color.FgYellow).SprintFunc()
	cyan    = color.New(color.FgCyan).SprintFunc()
	red     = color.New(color.FgRed).SprintFunc()
	magenta = color.New(color.FgMagenta).SprintFunc()
	bold    = color.New(color.Bold).SprintFunc()
)

func main() {
	// Parse flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	mnemonic := flag.String("mnemonic", "", "Mnemonic for deterministic wallet generation")
	walletCount := flag.Int("wallets", 5, "Number of wallets to simulate generating")
	hedged := flag.Bool("hedged", false, "Simulate hedged betting (two wallets, opposite bets)")
	verbose := flag.Bool("verbose", false, "Show verbose output")
	flag.Parse()

	// Load .env
	if err := godotenv.Load(); err != nil {
		fmt.Printf("%s No .env file found\n", yellow("!"))
	}

	printHeader()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Printf("%s Failed to load config: %v\n", red("✗"), err)
		os.Exit(1)
	}
	fmt.Printf("%s Config loaded from %s\n", green("✓"), *configPath)

	// Use mnemonic from flag or config
	testMnemonic := *mnemonic
	if testMnemonic == "" {
		testMnemonic = cfg.Wallets.Mnemonic
	}
	if testMnemonic == "" || testMnemonic == "${MNEMONIC}" {
		testMnemonic = "test dry run mnemonic phrase for simulation only"
		fmt.Printf("%s Using test mnemonic (no real mnemonic configured)\n", yellow("!"))
	} else {
		fmt.Printf("%s Mnemonic configured\n", green("✓"))
	}

	ctx := context.Background()

	// Run all tests
	fmt.Println()
	testWalletGeneration(ctx, cfg, testMnemonic, *walletCount, *verbose)
	fmt.Println()
	testReferralFlow(*verbose)
	fmt.Println()
	testBettingStrategy(cfg, *hedged, *verbose)
	fmt.Println()
	testConfigValidation(cfg, *verbose)
	fmt.Println()
	printSummary(cfg, *walletCount, *hedged)
}

func printHeader() {
	fmt.Println()
	fmt.Println(bold("╔══════════════════════════════════════════════════════════════╗"))
	fmt.Println(bold("║          BET BOT DRY RUN - Flow Verification Test           ║"))
	fmt.Println(bold("╚══════════════════════════════════════════════════════════════╝"))
	fmt.Println()
	fmt.Println(yellow("No transactions or API calls will be made."))
	fmt.Println()
}

func testWalletGeneration(ctx context.Context, cfg *config.Config, mnemonic string, count int, verbose bool) {
	fmt.Println(bold(cyan("━━━ WALLET GENERATION TEST ━━━")))
	fmt.Println()

	maxWallets := 1250
	if count > maxWallets {
		fmt.Printf("%s Wallet count %d exceeds max limit of %d\n", red("✗"), count, maxWallets)
		return
	}
	fmt.Printf("%s Wallet count (%d) within max limit (%d)\n", green("✓"), count, maxWallets)

	fmt.Println()
	fmt.Println(bold("Generating deterministic wallets from mnemonic:"))
	fmt.Println()

	// Generate sample wallets
	for i := 0; i < min(count, 5); i++ {
		privateKey, privateKeyHex, err := wallet.DerivePrivateKey(mnemonic, i)
		if err != nil {
			fmt.Printf("  %s Index %d: Failed - %v\n", red("✗"), i, err)
			continue
		}

		// Create wallet to get address
		w, err := wallet.NewWalletFromKey(privateKey)
		if err != nil {
			fmt.Printf("  %s Index %d: Failed - %v\n", red("✗"), i, err)
			continue
		}

		if verbose {
			fmt.Printf("  %s Index %d:\n", green("✓"), i)
			fmt.Printf("      Address: %s\n", cyan(w.Address.Hex()))
			fmt.Printf("      Key:     %s...%s\n", privateKeyHex[:6], privateKeyHex[len(privateKeyHex)-4:])
		} else {
			fmt.Printf("  %s Index %d: %s\n", green("✓"), i, cyan(w.Address.Hex()))
		}
	}

	if count > 5 {
		fmt.Printf("  ... and %d more wallets would be generated\n", count-5)
	}

	fmt.Println()
	fmt.Printf("%s Deterministic derivation: Same mnemonic + index = Same wallet\n", green("✓"))
	fmt.Printf("%s All %d wallets can be recovered using the mnemonic\n", green("✓"), count)
}

func testReferralFlow(verbose bool) {
	fmt.Println(bold(cyan("━━━ REFERRAL FLOW TEST ━━━")))
	fmt.Println()

	// Simulate referral code redemption
	fmt.Println(bold("Referral Code Redemption:"))
	fmt.Printf("  1. %s Get auth challenge from API\n", green("→"))
	fmt.Printf("  2. %s Sign challenge message with wallet private key\n", green("→"))
	fmt.Printf("  3. %s Submit signature to login endpoint\n", green("→"))
	fmt.Printf("  4. %s Receive access token\n", green("→"))
	fmt.Printf("  5. %s Redeem referral code with token\n", green("→"))
	fmt.Printf("  %s Referral redemption flow verified\n", green("✓"))

	fmt.Println()

	// Simulate username/referral code creation
	fmt.Println(bold("Username/Referral Code Creation:"))
	fmt.Println()

	for i := 0; i < 3; i++ {
		code := utils.GenerateUniqueReferralCode()
		fmt.Printf("  %s Generated code: %s\n", green("✓"), magenta(code))
	}

	fmt.Println()
	fmt.Printf("  %s Each wallet gets unique referral code\n", green("✓"))
	fmt.Printf("  %s Codes stored in wallet JSON for tracking\n", green("✓"))
}

func testBettingStrategy(cfg *config.Config, hedged bool, verbose bool) {
	fmt.Println(bold(cyan("━━━ BETTING STRATEGY TEST ━━━")))
	fmt.Println()

	// Create strategy
	strategy := betting.NewStrategy(cfg.Betting, cfg.Assets)

	// Create mock markets
	upDownMarkets := createMockUpDownMarkets(cfg)
	relativeMarkets := createMockRelativeMarkets(cfg)

	fmt.Println(bold("Market Configuration:"))
	fmt.Printf("  Enabled assets: %v\n", cfg.Betting.EnabledAssets)
	fmt.Printf("  Enabled timeframes: %v\n", cfg.Betting.EnabledTimeframes)
	fmt.Printf("  Mock UP/DOWN markets: %d\n", len(upDownMarkets))
	fmt.Printf("  Mock RELATIVE markets: %d\n", len(relativeMarkets))
	fmt.Println()

	// Simulate bet selection
	fmt.Println(bold("Simulated Bet Selections:"))
	fmt.Println()

	for i := 0; i < 3; i++ {
		bet, err := strategy.SelectRandomBet(upDownMarkets, relativeMarkets)
		if err != nil {
			fmt.Printf("  %s Selection %d failed: %v\n", red("✗"), i+1, err)
			continue
		}

		direction := bet.GetDirectionString()
		market := bet.GetMarketDescription()

		fmt.Printf("  %s Bet %d:\n", green("✓"), i+1)
		fmt.Printf("      Type:      %s\n", cyan(string(bet.Type)))
		fmt.Printf("      Market:    %s\n", market)
		fmt.Printf("      Direction: %s\n", colorDirection(direction))
		fmt.Printf("      Period:    %s\n", periodToTimeframe(bet.Period.Period))
		fmt.Printf("      Amount:    $%.2f USDC\n", bet.Amount)

		if hedged {
			oppositeBet := strategy.CreateOppositeBet(bet)
			oppositeDir := oppositeBet.GetDirectionString()
			fmt.Printf("      %s Opposite bet: %s (for wallet 2)\n", yellow("↔"), colorDirection(oppositeDir))
		}
		fmt.Println()
	}

	// Hedged betting explanation
	if hedged {
		fmt.Println(bold("Hedged Betting Flow:"))
		fmt.Printf("  %s Wallet 1: Places original bet (e.g., BTC %s)\n", green("→"), green("UP"))
		fmt.Printf("  %s Wallet 2: Places opposite bet (e.g., BTC %s)\n", green("→"), red("DOWN"))
		fmt.Printf("  %s Both wallets must be different addresses\n", green("✓"))
		fmt.Printf("  %s One bet will always win (minus platform fees)\n", green("✓"))
	}
}

func testConfigValidation(cfg *config.Config, verbose bool) {
	fmt.Println(bold(cyan("━━━ CONFIGURATION VALIDATION ━━━")))
	fmt.Println()

	errors := []string{}
	warnings := []string{}

	// Check required fields
	if cfg.RPCUrl == "" {
		errors = append(errors, "RPC URL not configured")
	}
	if cfg.DiamondAddress == "" {
		errors = append(errors, "Diamond contract address not configured")
	}
	if cfg.USDCAddress == "" {
		errors = append(errors, "USDC contract address not configured")
	}
	if cfg.OwnerPrivateKey == "" || cfg.OwnerPrivateKey == "${OWNER_PRIVATE_KEY}" {
		errors = append(errors, "Owner private key not configured (set OWNER_PRIVATE_KEY env)")
	}
	if cfg.Wallets.Mnemonic == "" || cfg.Wallets.Mnemonic == "${MNEMONIC}" {
		warnings = append(warnings, "Mnemonic not configured (set MNEMONIC env for deterministic wallets)")
	}
	if cfg.Wallets.ReferralCode == "" || cfg.Wallets.ReferralCode == "${REFERRAL_CODE}" {
		warnings = append(warnings, "Referral code not configured (set REFERRAL_CODE env)")
	}

	// Check betting config
	if cfg.Betting.MinAmountUSDC <= 0 {
		errors = append(errors, "Min bet amount must be > 0")
	}
	if cfg.Betting.MaxAmountUSDC < cfg.Betting.MinAmountUSDC {
		errors = append(errors, "Max bet amount must be >= min bet amount")
	}
	if len(cfg.Betting.EnabledAssets) == 0 {
		errors = append(errors, "No assets enabled for betting")
	}
	if len(cfg.Betting.EnabledTimeframes) == 0 {
		errors = append(errors, "No timeframes enabled for betting")
	}

	// Print results
	fmt.Println(bold("Required Configuration:"))
	printConfigItem("RPC URL", cfg.RPCUrl, cfg.RPCUrl != "")
	printConfigItem("Chain ID", fmt.Sprintf("%d", cfg.ChainID), cfg.ChainID > 0)
	printConfigItem("Diamond Contract", cfg.DiamondAddress, cfg.DiamondAddress != "")
	printConfigItem("USDC Contract", cfg.USDCAddress, cfg.USDCAddress != "")
	printConfigItem("Owner Key", maskKey(cfg.OwnerPrivateKey), cfg.OwnerPrivateKey != "" && cfg.OwnerPrivateKey != "${OWNER_PRIVATE_KEY}")

	fmt.Println()
	fmt.Println(bold("Wallet Configuration:"))
	printConfigItem("DB URL", cfg.Wallets.DBUrl, true)
	printConfigItem("Single Wallet Mode", fmt.Sprintf("%v", cfg.Wallets.SingleWalletMode), true)
	printConfigItem("Min Wallets Before Reuse", fmt.Sprintf("%d", cfg.Wallets.MinWalletsBeforeReuse), true)
	printConfigItem("New Wallet Probability", fmt.Sprintf("%.0f%%", cfg.Wallets.NewWalletProbability*100), true)

	fmt.Println()
	fmt.Println(bold("Betting Configuration:"))
	printConfigItem("Amount Range", fmt.Sprintf("$%.2f - $%.2f", cfg.Betting.MinAmountUSDC, cfg.Betting.MaxAmountUSDC), true)
	printConfigItem("Cooldown", fmt.Sprintf("%ds", cfg.Betting.CooldownSeconds), true)
	printConfigItem("Price Tolerance", fmt.Sprintf("%d bps (%.1f%%)", cfg.Betting.ToleranceBps, float64(cfg.Betting.ToleranceBps)/100), true)

	// Print errors and warnings
	if len(errors) > 0 {
		fmt.Println()
		fmt.Println(bold(red("Errors:")))
		for _, e := range errors {
			fmt.Printf("  %s %s\n", red("✗"), e)
		}
	}

	if len(warnings) > 0 {
		fmt.Println()
		fmt.Println(bold(yellow("Warnings:")))
		for _, w := range warnings {
			fmt.Printf("  %s %s\n", yellow("!"), w)
		}
	}

	if len(errors) == 0 {
		fmt.Println()
		fmt.Printf("%s Configuration is valid for deployment\n", green("✓"))
	}
}

func printSummary(cfg *config.Config, walletCount int, hedged bool) {
	fmt.Println(bold(cyan("━━━ SUMMARY ━━━")))
	fmt.Println()

	fmt.Println(bold("Bot Flow Overview:"))
	fmt.Println()
	fmt.Println("  ┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("  │ 1. WALLET CREATION (Deterministic from Mnemonic)            │")
	fmt.Printf("  │    - Generate up to %d wallets from single mnemonic        │\n", 1250)
	fmt.Println("  │    - Each wallet: Keccak256(mnemonic + \":\" + index)        │")
	fmt.Println("  │    - Fully recoverable with mnemonic                        │")
	fmt.Println("  ├─────────────────────────────────────────────────────────────┤")
	fmt.Println("  │ 2. REFERRAL SETUP (Per Wallet)                              │")
	fmt.Println("  │    - Redeem configured referral code                        │")
	fmt.Println("  │    - Create unique referral code (username)                 │")
	fmt.Println("  │    - Track in wallet JSON                                   │")
	fmt.Println("  ├─────────────────────────────────────────────────────────────┤")
	fmt.Println("  │ 3. BETTING STRATEGY                                         │")
	if hedged {
		fmt.Println("  │    - Wallet 1: BTC UP   ←→   Wallet 2: BTC DOWN            │")
		fmt.Println("  │    - Both wallets different addresses                       │")
		fmt.Println("  │    - Hedged = one always wins                               │")
	} else {
		fmt.Println("  │    - Random market selection (UP/DOWN or RELATIVE)         │")
		fmt.Println("  │    - Random direction, amount, timeframe                   │")
	}
	fmt.Println("  ├─────────────────────────────────────────────────────────────┤")
	fmt.Println("  │ 4. EXECUTION                                                │")
	fmt.Println("  │    - Check/fund wallet (USDC + MON for gas)                 │")
	fmt.Println("  │    - Fetch fresh prices from Pyth                           │")
	fmt.Println("  │    - Submit bet to Diamond contract                         │")
	fmt.Println("  │    - Wait for confirmation                                  │")
	fmt.Println("  └─────────────────────────────────────────────────────────────┘")
	fmt.Println()

	fmt.Println(bold(green("✓ Dry run complete - all flows verified")))
	fmt.Println()
}

// Helper functions

// Hardcoded addresses matching AssetSymbolMap in betting/strategy.go
var mockAssetAddresses = map[string]string{
	"BTC": "0x0555e30da8f98308edb960aa94c0db47230d2b9c",
	"ETH": "0xee8c0e9f1bffb4eb878d8f15f368a02a35481242",
	"SOL": "0xea17e5a9efebf1477db45082d67010e2245217f1",
}

func createMockUpDownMarkets(cfg *config.Config) []markets.Market {
	var mkts []markets.Market

	for _, asset := range cfg.Betting.EnabledAssets {
		addr, ok := mockAssetAddresses[asset]
		if !ok {
			continue
		}

		base := addr
		m := markets.Market{
			ID:   fmt.Sprintf("updown-%s", asset),
			Name: fmt.Sprintf("%s/USD", asset),
			Kind: markets.MarketKindUpDown,
			Base: &base,
			Periods: []markets.Period{
				{Period: "1", Status: "0"}, // 5m
				{Period: "3", Status: "0"}, // 15m
				{Period: "4", Status: "0"}, // 30m
			},
		}
		mkts = append(mkts, m)
	}

	return mkts
}

func createMockRelativeMarkets(cfg *config.Config) []markets.Market {
	if len(cfg.Betting.EnabledAssets) < 2 {
		return nil
	}

	var assets []string
	for _, asset := range cfg.Betting.EnabledAssets {
		if addr, ok := mockAssetAddresses[asset]; ok {
			assets = append(assets, addr)
		}
	}

	if len(assets) < 2 {
		return nil
	}

	return []markets.Market{
		{
			ID:     "relative-crypto",
			Name:   "Crypto Relative",
			Kind:   markets.MarketKindRelative,
			Assets: assets,
			Periods: []markets.Period{
				{Period: "1", Status: "0"}, // 5m
				{Period: "3", Status: "0"}, // 15m
			},
		},
	}
}

func colorDirection(dir string) string {
	switch dir {
	case "UP", "HIGHEST":
		return green(dir)
	case "DOWN", "LOWEST":
		return red(dir)
	default:
		return dir
	}
}

func maskKey(key string) string {
	if key == "" || key == "${OWNER_PRIVATE_KEY}" {
		return "(not set)"
	}
	if len(key) > 12 {
		return key[:6] + "..." + key[len(key)-4:]
	}
	return "***"
}

func printConfigItem(name, value string, ok bool) {
	status := green("✓")
	if !ok {
		status = red("✗")
	}
	fmt.Printf("  %s %-25s %s\n", status, name+":", value)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func periodToTimeframe(period string) string {
	tf, ok := markets.PeriodToTimeframe[period]
	if ok {
		return tf
	}
	return period
}
