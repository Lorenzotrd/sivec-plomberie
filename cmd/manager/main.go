package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"github.com/blinq-fi/blinq-mm-bot/internal/betting"
	"github.com/blinq-fi/blinq-mm-bot/internal/bot"
	"github.com/blinq-fi/blinq-mm-bot/internal/candlerush"
	"github.com/blinq-fi/blinq-mm-bot/internal/cli"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/contracts"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
	"github.com/blinq-fi/blinq-mm-bot/internal/notify"
	"github.com/blinq-fi/blinq-mm-bot/internal/prices"
	"github.com/blinq-fi/blinq-mm-bot/internal/secret"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		fmt.Printf("  %s No .env file found, using environment variables\n", cli.DimText("Note:"))
	}

	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	dryRun := flag.Bool("dry-run", false, "Simulation mode for all bots")
	generateCount := flag.Int("generate", 0, "Number of wallets to generate/ensure from mnemonic")
	flag.Parse()

	// Load configuration (manager-specific loader: skips owner_private_key validation)
	cfg, err := config.LoadManager(*configPath)
	if err != nil {
		fmt.Printf("  %s Failed to load config: %v\n", cli.Error("✗"), err)
		os.Exit(1)
	}

	// Determine enabled bots
	enabledBots := make(map[string]bool)
	for _, b := range cfg.Manager.EnabledBots {
		enabledBots[b] = true
	}

	fmt.Println(cli.Banner("BOT MANAGER STARTING"))
	fmt.Printf("  Enabled:  %s\n", cli.CyanText(strings.Join(cfg.Manager.EnabledBots, ", ")))
	fmt.Printf("  RPC:      %s\n", cli.DimText(cfg.MaskRPCUrl()))
	fmt.Printf("  Diamond:  %s\n", cli.DimText(cfg.DiamondAddress))
	fmt.Printf("  USDC:     %s\n", cli.DimText(cfg.USDCAddress))
	if *dryRun {
		fmt.Printf("\n  %s\n", cli.Warning("*** DRY RUN MODE - No transactions will be sent ***"))
	}
	fmt.Println()

	// ── Collect secrets (once) ──────────────────────────────────────────

	// Passphrase (required for bet-bot keystore; still load for manager)
	passphrase := wallet.LoadPassphrase()
	if passphrase == "" && enabledBots["bet-bot"] {
		fmt.Printf("  %s KEYSTORE_PASSPHRASE is required for bet-bot.\n", cli.Error("✗"))
		os.Exit(1)
	}
	var ks *wallet.Keystore
	if passphrase != "" {
		ks = wallet.NewKeystore(passphrase)
		fmt.Printf("  Keystore: %s\n", cli.Success("Enabled (keys encrypted at rest)"))
	}

	// Owner private key
	ownerKey := cfg.OwnerPrivateKey
	if ownerKey == "" || ownerKey == "0xyourprivatekeyhere" {
		ownerKey = secret.Load("owner_private_key", "OWNER_PRIVATE_KEY", "Enter owner wallet private key")
	}
	if ownerKey == "" {
		fmt.Printf("  %s OWNER_PRIVATE_KEY is required.\n", cli.Error("✗"))
		os.Exit(1)
	}

	// Resolve generate count: flag > config
	finalGenerateCount := 0
	if *generateCount > 0 {
		finalGenerateCount = *generateCount
	} else if cfg.Wallets.InitialWalletCount > 0 {
		finalGenerateCount = cfg.Wallets.InitialWalletCount
	}

	// Mnemonic (only if generating wallets)
	finalMnemonic := cfg.Wallets.Mnemonic
	if finalMnemonic == "" && finalGenerateCount > 0 {
		finalMnemonic = secret.Load("mnemonic", "MNEMONIC", "Enter mnemonic for wallet generation")
	}

	// ── Initialize owner wallet ─────────────────────────────────────────

	ownerWallet, err := wallet.NewWallet(ownerKey, cfg.RPCUrl, cfg.ChainID)
	if err != nil {
		fmt.Printf("  %s Failed to create owner wallet: %v\n", cli.Error("✗"), err)
		os.Exit(1)
	}
	// Zero out key material from memory now that the wallet is initialized
	ownerKey = ""
	cfg.OwnerPrivateKey = ""
	fmt.Printf("  Owner:    %s\n", cli.Address(ownerWallet.Address.Hex()))

	// ── Initialize wallet manager (for bet-bot) ─────────────────────────

	var walletManager *wallet.Manager
	if enabledBots["bet-bot"] {
		walletManager, err = wallet.NewManager(ownerWallet, cfg.Wallets, cfg.RPCUrl, cfg.ChainID, cfg.ApiBaseURL, ks)
		if err != nil {
			fmt.Printf("  %s Failed to create wallet manager: %v\n", cli.Error("✗"), err)
			os.Exit(1)
		}

		// Handle wallet generation if requested
		if finalGenerateCount > 0 {
			if finalMnemonic == "" {
				fmt.Printf("  %s Mnemonic is required for wallet generation\n", cli.Error("✗"))
				os.Exit(1)
			}
			fmt.Printf("  %s %s\n", cli.CyanText("Generating wallets from mnemonic..."), cli.DimText(fmt.Sprintf("(Target: %d)", finalGenerateCount)))
			added, err := walletManager.GenerateFromMnemonic(finalMnemonic, finalGenerateCount)
			if err != nil {
				fmt.Printf("  %s Failed to generate wallets: %v\n", cli.Error("✗"), err)
				os.Exit(1)
			}
			fmt.Printf("  %s Added %d new wallets\n", cli.Success("Done:"), added)
		}
		// Zero mnemonic after use
		finalMnemonic = ""
		cfg.Wallets.Mnemonic = ""

		fmt.Printf("  Wallets:  %s in store\n", cli.CyanText(fmt.Sprintf("%d", walletManager.WalletCount())))
	}

	fmt.Println(cli.Separator())
	fmt.Println()

	// ── Setup context + signal handling ─────────────────────────────────

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n  %s Received shutdown signal, stopping all bots...\n", cli.Warning("!"))
		cancel()
	}()

	// ── Launch enabled bots ─────────────────────────────────────────────

	var wg sync.WaitGroup

	if enabledBots["bet-bot"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runBetBot(ctx, cfg, walletManager, ks, ownerWallet, *dryRun)
		}()
	}

	if enabledBots["candle-rush-bot"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runCandleRushBot(ctx, cfg, ownerWallet, *dryRun)
		}()
	}

	// Launch periodic sweep if bet-bot is enabled (has sub-wallets to sweep)
	sweepHours := cfg.Manager.SweepIntervalHours
	if sweepHours == 0 {
		sweepHours = 8 // default
	}
	if enabledBots["bet-bot"] && ks != nil && sweepHours > 0 {
		sweepInterval := time.Duration(sweepHours) * time.Hour
		fmt.Printf("  %s %s Scheduled every %s\n", cli.Success("✓"), cli.BotPrefix("sweep"), sweepInterval)
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSweep(ctx, cfg, ownerWallet, ks, sweepInterval)
		}()
	}

	// Wait for all bots to finish
	wg.Wait()
	fmt.Println(cli.Banner("ALL BOTS STOPPED"))
}

// runBetBot runs the bet-bot logic (extracted from cmd/bot/main.go)
func runBetBot(ctx context.Context, cfg *config.Config, walletManager *wallet.Manager, ks *wallet.Keystore, ownerWallet *wallet.Wallet, dryRun bool) {
	fmt.Println(cli.Banner("BET-BOT STARTING"))

	// Priority: Config > Default for threads
	threads := 1
	if cfg.Threads > 1 {
		threads = cfg.Threads
	}
	if threads > 1 {
		fmt.Printf("  Threads:  %s\n", cli.CyanText(fmt.Sprintf("%d", threads)))
	}

	fmt.Printf("  Cooldown: %s\n", cli.CyanText(fmt.Sprintf("%d seconds", cfg.Betting.CooldownSeconds)))
	fmt.Printf("  Amount:   %s - %s\n", cli.Amount(cfg.Betting.MinAmountUSDC), cli.Amount(cfg.Betting.MaxAmountUSDC))
	if !cfg.Wallets.ShouldGenerateNewWallets() {
		fmt.Printf("  Wallets:  %s\n", cli.Warning("Using existing wallets only (no new generation)"))
	}
	if cfg.ApiBaseURL == "" {
		fmt.Printf("  Referral: %s\n", cli.DimText("Disabled (API_BASE_URL not set)"))
	} else if cfg.Wallets.ReferralCode != "" && cfg.Wallets.ReferralCode != "${REFERRAL_CODE}" {
		fmt.Printf("  Referral: %s\n", cli.Success("Configured"))
	} else {
		fmt.Printf("  Referral: %s\n", cli.DimText("Not configured"))
	}
	if dryRun {
		fmt.Printf("\n  %s\n", cli.Warning("*** DRY RUN MODE ***"))
	}
	fmt.Println()

	// Initialize Ethereum client
	client, err := ethclient.Dial(cfg.RPCUrl)
	if err != nil {
		fmt.Printf("  %s %s Failed to connect to RPC: %v\n", cli.Error("✗"), cli.BotPrefix("bet-bot"), err)
		return
	}
	defer client.Close()

	// Initialize contracts
	diamondAddr := common.HexToAddress(cfg.DiamondAddress)
	usdcAddr := common.HexToAddress(cfg.USDCAddress)

	diamond, err := contracts.NewDiamond(diamondAddr, client)
	if err != nil {
		fmt.Printf("  %s %s Failed to create Diamond contract: %v\n", cli.Error("✗"), cli.BotPrefix("bet-bot"), err)
		return
	}

	usdc, err := contracts.NewERC20(usdcAddr, client)
	if err != nil {
		fmt.Printf("  %s %s Failed to create USDC contract: %v\n", cli.Error("✗"), cli.BotPrefix("bet-bot"), err)
		return
	}

	// Initialize market fetcher
	marketFetcher := markets.NewFetcher(cfg.HasuraURL, "")

	// Initialize price fetcher
	var priceFetcher *prices.Fetcher
	if cfg.UseStreaming {
		priceIDs := make([]string, 0, len(cfg.Assets))
		for _, asset := range cfg.Assets {
			priceIDs = append(priceIDs, asset.PriceID)
		}
		priceFetcher = prices.NewFetcherWithStreaming(cfg.HermesURL, priceIDs)
		fmt.Printf("  %s %s Streaming price fetcher with %d price IDs\n", cli.Success("✓"), cli.BotPrefix("bet-bot"), len(priceIDs))
	} else {
		priceFetcher = prices.NewFetcher(cfg.HermesURL)
		fmt.Printf("  %s %s HTTP-only price fetcher\n", cli.Success("✓"), cli.BotPrefix("bet-bot"))
	}
	defer func() {
		if priceFetcher != nil {
			priceFetcher.Close()
		}
	}()

	// Initialize strategy
	strategy := betting.NewStrategy(cfg.Betting, cfg.Assets)

	// Initialize executor
	executor := betting.NewExecutorWithOptions(
		diamond,
		usdc,
		priceFetcher,
		usdcAddr,
		diamondAddr,
		cfg.Betting.ToleranceBps,
		cfg.Betting.BrokerID,
		cfg.GetMaxPriceAge(),
		cfg.GetPriceRetries(),
	)

	fmt.Printf("  %s Tolerance: %s | Max Price Age: %s\n", cli.BotPrefix("bet-bot"),
		cli.CyanText(fmt.Sprintf("%d bps (%0.1f%%)", cfg.Betting.ToleranceBps, float64(cfg.Betting.ToleranceBps)/100)),
		cli.CyanText(cfg.GetMaxPriceAge().String()))

	// Slack notifier
	slackNotifier := notify.NewSlackNotifier(os.Getenv("SLACK_WEBHOOK_URL"))

	lowBalanceUSDC := 10.0
	if v := os.Getenv("LOW_BALANCE_THRESHOLD_USDC"); v != "" {
		fmt.Sscanf(v, "%f", &lowBalanceUSDC)
	}
	lowBalanceMON := 1.0
	if v := os.Getenv("LOW_BALANCE_THRESHOLD_MON"); v != "" {
		fmt.Sscanf(v, "%f", &lowBalanceMON)
	}

	// Check owner balances
	ownerUSDCBalance, err := executor.CheckBalance(ctx, ownerWallet.Address)
	if err != nil {
		fmt.Printf("  %s %s Could not check owner USDC balance: %v\n", cli.Warning("Warning:"), cli.BotPrefix("bet-bot"), err)
	} else {
		fmt.Printf("  %s Balance: %s\n", cli.BotPrefix("bet-bot"), cli.Amount(ownerUSDCBalance))
		if ownerUSDCBalance < lowBalanceUSDC {
			fmt.Printf("  %s %s Owner USDC balance is below threshold (%.2f < %.2f)\n", cli.Warning("!"), cli.BotPrefix("bet-bot"), ownerUSDCBalance, lowBalanceUSDC)
			if err := slackNotifier.SendLowBalanceAlert(ownerWallet.Address.Hex(), "USDC", ownerUSDCBalance, lowBalanceUSDC, "bet-bot"); err != nil {
				fmt.Printf("  %s Failed to send Slack alert: %v\n", cli.Warning("!"), err)
			}
		}
	}

	ownerMONBalance, err := executor.CheckNativeBalance(ctx, ownerWallet, ownerWallet.Address)
	if err != nil {
		fmt.Printf("  %s %s Could not check owner MON balance: %v\n", cli.Warning("Warning:"), cli.BotPrefix("bet-bot"), err)
	} else {
		fmt.Printf("  %s Balance: %s\n", cli.BotPrefix("bet-bot"), cli.AmountMON(ownerMONBalance))
		if ownerMONBalance < lowBalanceMON {
			fmt.Printf("  %s %s Owner MON balance is below threshold (%.4f < %.4f)\n", cli.Warning("!"), cli.BotPrefix("bet-bot"), ownerMONBalance, lowBalanceMON)
			if err := slackNotifier.SendLowBalanceAlert(ownerWallet.Address.Hex(), "MON", ownerMONBalance, lowBalanceMON, "bet-bot"); err != nil {
				fmt.Printf("  %s Failed to send Slack alert: %v\n", cli.Warning("!"), err)
			}
		}
	}

	fmt.Println(cli.Separator())
	fmt.Println()

	// Launch worker threads
	// Always use hedged mode in manager (matches docker-compose default)
	hedged := true

	var workerWg sync.WaitGroup
	for i := 0; i < threads; i++ {
		workerWg.Add(1)
		go func(workerID int) {
			defer workerWg.Done()

			// Add jitter to start time
			jitter := time.Duration(rand.Intn(5000)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}

			betCount := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				prefix := cli.BotPrefix("bet-bot") + " "
				if threads > 1 {
					prefix = cli.BotPrefix(fmt.Sprintf("bet-bot/W%d", workerID)) + " "
				}

				fmt.Printf("%s%s\n", prefix, cli.CycleHeader(betCount+1))
				err := bot.RunBettingCycle(ctx, cfg, walletManager, marketFetcher, strategy, executor, dryRun, hedged)
				if err != nil {
					fmt.Printf("%s  %s %v\n", prefix, cli.Error("Error:"), err)
				} else {
					betCount++
				}
				fmt.Printf("%s%s\n", prefix, cli.CycleFooter())

				select {
				case <-ctx.Done():
					return
				case <-time.After(cfg.GetCooldown()):
				}
			}
		}(i + 1)
	}

	workerWg.Wait()
	fmt.Println(cli.Banner("BET-BOT STOPPED"))
}

// intervalStats tracks per-interval betting statistics.
type intervalStats struct {
	mu        sync.Mutex
	total     int
	success   int
	failed    int
	interval  uint32
}

func (s *intervalStats) add(success, failed int) {
	s.mu.Lock()
	s.total += success + failed
	s.success += success
	s.failed += failed
	s.mu.Unlock()
}

func (s *intervalStats) get() (total, success, failed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.success, s.failed
}

// runCandleRushBot runs the candle-rush-bot logic with independent per-interval goroutines.
func runCandleRushBot(ctx context.Context, cfg *config.Config, ownerWallet *wallet.Wallet, dryRun bool) {
	fmt.Println(cli.Banner("CANDLE-RUSH-BOT STARTING"))

	cr := cfg.CandleRush

	// Resolve intervals
	intervals := make([]uint32, len(cr.Intervals))
	for i, v := range cr.Intervals {
		intervals[i] = uint32(v)
	}
	if len(intervals) == 0 {
		fmt.Printf("  %s %s No intervals configured\n", cli.Error("✗"), cli.BotPrefix("candle-rush"))
		return
	}

	// Resolve candles per interval
	candlesPerInterval := cr.CandlesPerInterval
	if candlesPerInterval < 1 {
		candlesPerInterval = 5
	}

	// Resolve amounts
	minAmount := cr.MinAmountUSDC
	maxAmount := cr.MaxAmountUSDC
	if minAmount <= 0 {
		minAmount = 2.0
	}
	if maxAmount <= 0 {
		maxAmount = 10.0
	}

	// Coverage-based halt time config
	visibleCandles := cr.VisibleCandles
	if visibleCandles <= 0 {
		visibleCandles = 14
	}
	targetCovered := cr.TargetCovered
	if targetCovered <= 0 {
		targetCovered = 8
	}
	if targetCovered >= visibleCandles {
		fmt.Printf("  %s %s target_covered (%d) must be < visible_candles (%d)\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), targetCovered, visibleCandles)
		return
	}
	gapRatio := float64(visibleCandles-targetCovered) / float64(targetCovered)

	// Resolve asset addresses
	assetAddresses := map[string]string{
		"BTC": "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c",
		"ETH": "0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242",
		"SOL": "0xea17E5a9efEBf1477dB45082d67010E2245217f1",
	}
	// Override from config assets map
	for name, asset := range cfg.Assets {
		assetAddresses[strings.ToUpper(name)] = asset.Address
	}

	assetNames := cr.Assets
	if len(assetNames) == 0 {
		assetNames = []string{"BTC", "ETH", "SOL"}
	}

	var resolvedAssets []common.Address
	for _, name := range assetNames {
		if addr, ok := assetAddresses[strings.ToUpper(name)]; ok {
			resolvedAssets = append(resolvedAssets, common.HexToAddress(addr))
		} else if common.IsHexAddress(name) {
			resolvedAssets = append(resolvedAssets, common.HexToAddress(name))
		} else {
			fmt.Printf("  %s %s Unknown asset: %s\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), name)
			return
		}
	}
	if len(resolvedAssets) == 0 {
		fmt.Printf("  %s %s No assets configured\n", cli.Error("✗"), cli.BotPrefix("candle-rush"))
		return
	}

	brokerID := cr.BrokerID
	if brokerID <= 0 {
		brokerID = 1
	}

	crPrefix := cli.BotPrefix("candle-rush")
	fmt.Printf("  %s Assets:     %s\n", crPrefix, cli.CyanText(strings.Join(assetNames, ", ")))
	fmt.Printf("  %s Intervals:  %s\n", crPrefix, cli.CyanText(formatIntervals(intervals)))
	fmt.Printf("  %s Candles:    %s per interval\n", crPrefix, cli.CyanText(fmt.Sprintf("%d", candlesPerInterval)))
	fmt.Printf("  %s Amount:     %s - %s\n", crPrefix, cli.Amount(minAmount), cli.Amount(maxAmount))
	fmt.Printf("  %s Coverage:   %d/%d candles (gap ratio: %.2f)\n", crPrefix, targetCovered, visibleCandles, gapRatio)

	// Log per-interval halt durations
	for _, interval := range intervals {
		haltDuration := time.Duration(float64(candlesPerInterval)*float64(interval)*gapRatio) * time.Second
		fmt.Printf("  %s Halt %s:   %s\n", crPrefix, intervalToName(interval), haltDuration.Round(time.Second))
	}

	if dryRun {
		fmt.Printf("\n  %s\n", cli.Warning("*** DRY RUN MODE ***"))
	}
	fmt.Println()

	// Connect to RPC
	client, err := ethclient.Dial(cfg.RPCUrl)
	if err != nil {
		fmt.Printf("  %s %s Failed to connect to RPC: %v\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), err)
		return
	}
	defer client.Close()

	// Initialize contracts
	diamondAddr := common.HexToAddress(cfg.DiamondAddress)
	usdcAddr := common.HexToAddress(cfg.USDCAddress)

	candleRushContract, err := contracts.NewCandleRush(diamondAddr, client)
	if err != nil {
		fmt.Printf("  %s %s Failed to initialize CandleRush contract: %v\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), err)
		return
	}

	usdcContract, err := contracts.NewERC20(usdcAddr, client)
	if err != nil {
		fmt.Printf("  %s %s Failed to initialize USDC contract: %v\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), err)
		return
	}

	executor := candlerush.NewExecutor(candleRushContract, usdcContract, usdcAddr, diamondAddr, brokerID)
	executor.SimulationMode = dryRun

	// Slack notifier
	slackNotifier := notify.NewSlackNotifier(os.Getenv("SLACK_WEBHOOK_URL"))

	lowBalanceUSDC := 50.0
	if v := os.Getenv("LOW_BALANCE_THRESHOLD_USDC"); v != "" {
		fmt.Sscanf(v, "%f", &lowBalanceUSDC)
	}

	// Check initial balance
	balance, err := executor.CheckBalance(ctx, ownerWallet.Address)
	if err != nil {
		fmt.Printf("  %s %s Failed to check balance: %v\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), err)
		return
	}
	fmt.Printf("  %s %s USDC Balance: %s\n", cli.Success("✓"), cli.BotPrefix("candle-rush"), cli.CyanText(fmt.Sprintf("%.2f", balance)))

	if balance < lowBalanceUSDC {
		fmt.Printf("  %s %s USDC balance below threshold (%.2f < %.2f)\n", cli.Warning("!"), cli.BotPrefix("candle-rush"), balance, lowBalanceUSDC)
		if err := slackNotifier.SendLowBalanceAlert(ownerWallet.Address.Hex(), "USDC", balance, lowBalanceUSDC, "candle-rush-bot"); err != nil {
			fmt.Printf("  %s Failed to send Slack alert: %v\n", cli.Warning("!"), err)
		}
	}

	// Verify minimum balance covers ALL intervals for one round at max amount
	minRequired := maxAmount * 2 * float64(len(resolvedAssets)) * float64(candlesPerInterval) * float64(len(intervals))
	if balance < minRequired {
		fmt.Printf("  %s %s Insufficient balance: need at least %.2f USDC (for worst-case round)\n", cli.Error("✗"), cli.BotPrefix("candle-rush"), minRequired)
		return
	}

	startTime := time.Now()

	// Create per-interval stats
	allStats := make([]*intervalStats, len(intervals))
	for i, interval := range intervals {
		allStats[i] = &intervalStats{interval: interval}
	}

	fmt.Printf("  %s %s Starting %d independent interval goroutines...\n", cli.Success("►"), cli.BotPrefix("candle-rush"), len(intervals))
	fmt.Println()

	// Launch one goroutine per interval
	var crWg sync.WaitGroup
	for i, interval := range intervals {
		crWg.Add(1)
		go func(interval uint32, stats *intervalStats) {
			defer crWg.Done()
			runCandleRushInterval(ctx, executor, ownerWallet, resolvedAssets,
				interval, candlesPerInterval, minAmount, maxAmount,
				gapRatio, slackNotifier, lowBalanceUSDC, stats)
		}(interval, allStats[i])
	}
	crWg.Wait()

	printCandleRushStats(allStats, startTime)
}

// runCandleRushInterval runs the betting loop for a single interval independently.
func runCandleRushInterval(
	ctx context.Context,
	executor *candlerush.Executor,
	ownerWallet *wallet.Wallet,
	assets []common.Address,
	interval uint32,
	candlesPerInterval int,
	minAmount, maxAmount float64,
	gapRatio float64,
	slackNotifier *notify.SlackNotifier,
	lowBalanceUSDC float64,
	stats *intervalStats,
) {
	prefix := cli.BotPrefix(fmt.Sprintf("candle-rush/%s", intervalToName(interval)))
	betsPerRound := len(assets) * candlesPerInterval * 2
	haltDuration := time.Duration(float64(candlesPerInterval)*float64(interval)*gapRatio) * time.Second

	var lastBetOpenTime uint64

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		amount := candlerush.GetRandomAmount(minAmount, maxAmount)
		totalNeeded := amount * float64(betsPerRound)

		// Pre-check balance for this interval's round
		balance, err := executor.CheckBalance(ctx, ownerWallet.Address)
		if err != nil {
			fmt.Printf("  %s %s Balance check failed: %v\n", cli.Error("✗"), prefix, err)
			// Wait before retrying
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}

		if balance < totalNeeded {
			fmt.Printf("  %s %s Insufficient balance: need %.2f USDC, have %.2f\n", cli.Warning("!"), prefix, totalNeeded, balance)
			if err := slackNotifier.SendLowBalanceAlert(ownerWallet.Address.Hex(), "USDC", balance, totalNeeded, "candle-rush-bot"); err != nil {
				fmt.Printf("  %s Failed to send Slack alert: %v\n", cli.Warning("!"), err)
			}
			// Wait before retrying
			select {
			case <-ctx.Done():
				return
			case <-time.After(60 * time.Second):
			}
			continue
		}

		fmt.Printf("  %s Round: %d assets × %d candles × 2 sides @ %.2f USDC = %.2f USDC needed\n",
			prefix, len(assets), candlesPerInterval, amount, totalNeeded)

		results, err := executor.PlaceAllAssetsMultiCandle(ctx, ownerWallet, assets, interval, candlesPerInterval, amount, lastBetOpenTime)

		if err != nil {
			fmt.Printf("  %s %s Error: %v\n", cli.Error("✗"), prefix, err)
			stats.add(0, betsPerRound)
		} else {
			stats.add(betsPerRound, 0)
			if len(results) > 0 {
				firstOpen := results[0].OpenTime
				lastOpen := results[len(results)-1].OpenTime
				fmt.Printf("  %s %s %d bets placed | candles: %s → %s\n",
					cli.Success("✓"), prefix, betsPerRound,
					candlerush.FormatOpenTime(firstOpen),
					candlerush.FormatOpenTime(lastOpen))
				lastBetOpenTime = lastOpen
			}
		}

		// Post-round balance check
		if postBalance, err := executor.CheckBalance(ctx, ownerWallet.Address); err == nil {
			fmt.Printf("  %s Post-round USDC: %.2f\n", prefix, postBalance)
			if postBalance < lowBalanceUSDC {
				if err := slackNotifier.SendLowBalanceAlert(ownerWallet.Address.Hex(), "USDC", postBalance, lowBalanceUSDC, "candle-rush-bot"); err != nil {
					fmt.Printf("  %s Failed to send Slack alert: %v\n", cli.Warning("!"), err)
				}
			}
		}

		fmt.Printf("  %s Halting for %s\n", prefix, haltDuration.Round(time.Second))

		select {
		case <-ctx.Done():
			return
		case <-time.After(haltDuration):
		}
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

func intervalToName(interval uint32) string {
	switch interval {
	case 300:
		return "5m"
	case 900:
		return "15m"
	case 1800:
		return "30m"
	default:
		return fmt.Sprintf("%ds", interval)
	}
}

func formatIntervals(intervals []uint32) string {
	strs := make([]string, len(intervals))
	for i, interval := range intervals {
		strs[i] = intervalToName(interval)
	}
	return strings.Join(strs, ", ")
}

func printCandleRushStats(allStats []*intervalStats, startTime time.Time) {
	fmt.Printf("  %s Final stats:\n", cli.BotPrefix("candle-rush"))

	var grandTotal, grandSuccess, grandFailed int
	for _, s := range allStats {
		total, success, failed := s.get()
		grandTotal += total
		grandSuccess += success
		grandFailed += failed
		fmt.Printf("    %s:  %d bets | %d success | %d failed\n",
			intervalToName(s.interval), total, success, failed)
	}

	fmt.Printf("    Total: %d bets | %d success | %d failed | Runtime: %s\n",
		grandTotal, grandSuccess, grandFailed, time.Since(startTime).Round(time.Second))
	fmt.Println(cli.Banner("CANDLE-RUSH-BOT STOPPED"))
}

// ── Sweep ───────────────────────────────────────────────────────────────

// runSweep runs the sweep job on a ticker, sweeping USDC from sub-wallets back to owner.
func runSweep(ctx context.Context, cfg *config.Config, ownerWallet *wallet.Wallet, ks *wallet.Keystore, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Println(cli.Banner("SWEEP STARTING"))
			runSweepOnce(ctx, cfg, ownerWallet, ks)
			fmt.Println(cli.Banner("SWEEP COMPLETE"))
		}
	}
}

// runSweepOnce sweeps USDC (and MON) from all sub-wallets back to the owner wallet.
// Extracted from cmd/sweep-all logic.
func runSweepOnce(ctx context.Context, cfg *config.Config, ownerWallet *wallet.Wallet, ks *wallet.Keystore) {
	sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	client, err := ethclient.Dial(cfg.RPCUrl)
	if err != nil {
		fmt.Printf("  %s %s Failed to connect to RPC: %v\n", cli.Error("✗"), cli.BotPrefix("sweep"), err)
		return
	}
	defer client.Close()

	usdcAddr := common.HexToAddress(cfg.USDCAddress)
	usdc, err := contracts.NewERC20(usdcAddr, client)
	if err != nil {
		fmt.Printf("  %s %s Failed to create USDC contract: %v\n", cli.Error("✗"), cli.BotPrefix("sweep"), err)
		return
	}

	store, err := wallet.NewStore(cfg.Wallets.DBUrl, ks)
	if err != nil {
		fmt.Printf("  %s %s Failed to load wallet store: %v\n", cli.Error("✗"), cli.BotPrefix("sweep"), err)
		return
	}
	defer store.Close()

	wallets := store.GetAllWallets()
	fmt.Printf("  %s Scanning %d wallets...\n", cli.BotPrefix("sweep"), len(wallets))

	minUSDCWei := wallet.ToBaseUnits(0.01, 6) // sweep anything above 0.01 USDC
	minMONWei := wallet.ToWei(0.001)           // sweep anything above 0.001 MON
	reserveGasWei := wallet.ToWei(0.001)       // keep 0.001 MON for future gas

	gasPrice, _ := client.SuggestGasPrice(sweepCtx)
	nativeGasCost := new(big.Int).Mul(gasPrice, big.NewInt(21000))
	usdcGasCost := new(big.Int).Mul(gasPrice, big.NewInt(120000))

	var totalUSDCSwept float64
	var totalMONSwept float64
	var walletsSweptUSDC, walletsSweptMON, errors int

	for i, sw := range wallets {
		select {
		case <-sweepCtx.Done():
			fmt.Printf("  %s %s Interrupted\n", cli.Warning("!"), cli.BotPrefix("sweep"))
			goto sweepDone
		default:
		}

		walletAddr := common.HexToAddress(sw.Address)
		prefix := fmt.Sprintf("  %s %s", cli.BotPrefix(fmt.Sprintf("sweep %d/%d", i+1, len(wallets))), truncateAddr(sw.Address))

		// Check USDC balance
		usdcBalance, err := usdc.BalanceOf(sweepCtx, walletAddr)
		if err != nil {
			errors++
			continue
		}

		// Check MON balance
		monBalance, err := client.BalanceAt(sweepCtx, walletAddr, nil)
		if err != nil {
			errors++
			continue
		}

		hasUSDC := usdcBalance.Cmp(minUSDCWei) > 0
		hasMON := monBalance.Cmp(minMONWei) > 0

		if !hasUSDC && !hasMON {
			continue
		}

		usdcFloat := wallet.FromBaseUnits(usdcBalance, 6)
		monFloat := wallet.FromWei(monBalance)
		fmt.Printf("%s - %.4f USDC, %.4f MON\n", prefix, usdcFloat, monFloat)

		// Decrypt private key
		privateKey := sw.PrivateKey
		if ks.Enabled() {
			privateKey, err = ks.Decrypt(privateKey)
			if err != nil {
				fmt.Printf("%s %s decrypt error: %v\n", prefix, cli.Error("✗"), err)
				errors++
				continue
			}
		}

		w, err := wallet.NewWallet(privateKey, cfg.RPCUrl, cfg.ChainID)
		if err != nil {
			fmt.Printf("%s %s wallet error: %v\n", prefix, cli.Error("✗"), err)
			errors++
			continue
		}

		// Sweep USDC first (needs gas)
		if hasUSDC {
			// Check if wallet has enough gas for USDC transfer
			if monBalance.Cmp(usdcGasCost) < 0 {
				// Fund gas from owner
				fundAmount := new(big.Int).Set(usdcGasCost)
				txHash, err := ownerWallet.SendNative(sweepCtx, walletAddr, fundAmount)
				if err != nil {
					fmt.Printf("%s %s gas funding error: %v\n", prefix, cli.Error("✗"), err)
					errors++
					continue
				}
				receipt, err := ownerWallet.WaitForTx(sweepCtx, txHash)
				if err != nil || receipt.Status == 0 {
					fmt.Printf("%s %s gas funding tx failed\n", prefix, cli.Error("✗"))
					errors++
					continue
				}
				fmt.Printf("%s %s funded gas\n", prefix, cli.Success("✓"))
				time.Sleep(500 * time.Millisecond)
			}

			opts, err := w.GetTransactOpts(sweepCtx)
			if err != nil {
				fmt.Printf("%s %s tx opts error: %v\n", prefix, cli.Error("✗"), err)
				errors++
				continue
			}

			tx, err := usdc.Transfer(sweepCtx, opts, ownerWallet.Address, usdcBalance)
			if err != nil {
				fmt.Printf("%s %s USDC transfer error: %v\n", prefix, cli.Error("✗"), err)
				errors++
				continue
			}

			receipt, err := w.WaitForTx(sweepCtx, tx.Hash())
			if err != nil || receipt.Status == 0 {
				fmt.Printf("%s %s USDC tx failed\n", prefix, cli.Error("✗"))
				errors++
				continue
			}

			fmt.Printf("%s %s swept %.4f USDC\n", prefix, cli.Success("✓"), usdcFloat)
			totalUSDCSwept += usdcFloat
			walletsSweptUSDC++
			time.Sleep(500 * time.Millisecond)
		}

		// Sweep MON (re-fetch balance after USDC sweep may have used gas)
		currentMON, err := client.BalanceAt(sweepCtx, walletAddr, nil)
		if err != nil || currentMON.Cmp(minMONWei) <= 0 {
			continue
		}

		sweepableMON := new(big.Int).Sub(currentMON, nativeGasCost)
		sweepableMON = sweepableMON.Sub(sweepableMON, reserveGasWei)
		if sweepableMON.Cmp(big.NewInt(0)) <= 0 {
			continue
		}

		txHash, err := w.SendNative(sweepCtx, ownerWallet.Address, sweepableMON)
		if err != nil {
			fmt.Printf("%s %s MON transfer error: %v\n", prefix, cli.Error("✗"), err)
			errors++
			continue
		}

		receipt, err := w.WaitForTx(sweepCtx, txHash)
		if err != nil || receipt.Status == 0 {
			fmt.Printf("%s %s MON tx failed\n", prefix, cli.Error("✗"))
			errors++
			continue
		}

		monSwept := wallet.FromWei(sweepableMON)
		fmt.Printf("%s %s swept %.4f MON\n", prefix, cli.Success("✓"), monSwept)
		totalMONSwept += monSwept
		walletsSweptMON++
		time.Sleep(500 * time.Millisecond)
	}

sweepDone:
	fmt.Printf("  %s Summary: %d USDC wallets (%.4f USDC) | %d MON wallets (%.4f MON) | %d errors\n", cli.BotPrefix("sweep"),
		walletsSweptUSDC, totalUSDCSwept, walletsSweptMON, totalMONSwept, errors)
}

func truncateAddr(addr string) string {
	if len(addr) > 12 {
		return addr[:6] + "..." + addr[len(addr)-4:]
	}
	return addr
}
