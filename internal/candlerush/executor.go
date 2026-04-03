package candlerush

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/fatih/color"

	"github.com/blinq-fi/blinq-mm-bot/internal/contracts"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

var (
	crGreen  = color.New(color.FgGreen).SprintFunc()
	crYellow = color.New(color.FgYellow).SprintFunc()
	crCyan   = color.New(color.FgCyan).SprintFunc()
	crDim    = color.New(color.Faint).SprintFunc()
)

// Executor handles Candle Rush bet placement
type Executor struct {
	candleRush     *contracts.CandleRush
	usdc           *contracts.ERC20
	usdcAddress    common.Address
	diamondAddress common.Address
	brokerID       int
	SimulationMode bool
}

// Config holds the configuration for Candle Rush betting
type Config struct {
	AssetAddress    common.Address // The asset to bet on (e.g., BTC)
	MinAmountUSDC   float64        // Minimum bet amount
	MaxAmountUSDC   float64        // Maximum bet amount
	Intervals       []uint32       // Enabled intervals (300, 900, 1800)
	CooldownSeconds int            // Cooldown between bets
	BrokerID        int            // Broker ID
}

// BetResult contains the result of a placed bet
type BetResult struct {
	TxHash    string
	Side      contracts.CandleRushSide
	Interval  contracts.CandleRushInterval
	OpenTime  uint64
	Amount    float64
	Confirmed bool
	Error     error
}

// NewExecutor creates a new Candle Rush executor
func NewExecutor(
	candleRush *contracts.CandleRush,
	usdc *contracts.ERC20,
	usdcAddress, diamondAddress common.Address,
	brokerID int,
) *Executor {
	return &Executor{
		candleRush:     candleRush,
		usdc:           usdc,
		usdcAddress:    usdcAddress,
		diamondAddress: diamondAddress,
		brokerID:       brokerID,
	}
}

// CalculateNextOpenTime calculates the next candle open time for a given interval
func CalculateNextOpenTime(intervalSeconds uint32) uint64 {
	now := time.Now().UTC().Unix()
	interval := int64(intervalSeconds)

	// Calculate the next slot's open time
	// Current bucket = now / interval
	// Next open = (current bucket + 1) * interval
	currentBucket := now / interval
	nextOpen := (currentBucket + 1) * interval

	return uint64(nextOpen)
}

// CalculateNextOpenTimeAfter calculates the next candle open time that is strictly
// after the given timestamp. This prevents re-betting on candles from a previous round.
func CalculateNextOpenTimeAfter(intervalSeconds uint32, afterTimestamp uint64) uint64 {
	nextOpen := CalculateNextOpenTime(intervalSeconds)

	// If the next open time is at or before the last bet, advance until it's after
	interval := uint64(intervalSeconds)
	for nextOpen <= afterTimestamp {
		nextOpen += interval
	}

	return nextOpen
}

// GetRandomInterval returns a random interval from the enabled list
func GetRandomInterval(intervals []uint32) uint32 {
	if len(intervals) == 0 {
		// Default to all intervals
		intervals = []uint32{300, 900, 1800}
	}
	return intervals[rand.Intn(len(intervals))]
}

// GetRandomAmount returns a random bet amount between min and max
func GetRandomAmount(min, max float64) float64 {
	if min >= max {
		return min
	}
	return min + rand.Float64()*(max-min)
}

// PlaceBothSides places bets on both GREEN and RED sides for the same candle
// This is the core bot functionality - betting on both sides
func (e *Executor) PlaceBothSides(
	ctx context.Context,
	w *wallet.Wallet,
	assetAddress common.Address,
	intervalSeconds uint32,
	amount float64,
) (greenResult, redResult *BetResult, err error) {
	// Calculate the next open time for this interval
	openTime := CalculateNextOpenTime(intervalSeconds)

	// Check if we have enough time before the candle opens
	now := time.Now().UTC().Unix()
	timeUntilOpen := int64(openTime) - now
	if timeUntilOpen < 10 {
		// Less than 10 seconds, skip to next candle
		openTime = openTime + uint64(intervalSeconds)
		fmt.Printf("    %s Too close to candle open, using next candle at %d\n", crYellow("!"), openTime)
	}

	amountBase := wallet.ToBaseUnits(amount, 6)
	totalNeeded := new(big.Int).Mul(amountBase, big.NewInt(2)) // Need 2x for both sides

	// Initialize results
	greenResult = &BetResult{
		Side:     contracts.CandleRushSideUp,
		Interval: contracts.CandleRushInterval(intervalSeconds),
		OpenTime: openTime,
		Amount:   amount,
	}
	redResult = &BetResult{
		Side:     contracts.CandleRushSideDown,
		Interval: contracts.CandleRushInterval(intervalSeconds),
		OpenTime: openTime,
		Amount:   amount,
	}

	if e.SimulationMode {
		greenResult.TxHash = "0xSIMULATED_GREEN_TX"
		greenResult.Confirmed = true
		redResult.TxHash = "0xSIMULATED_RED_TX"
		redResult.Confirmed = true
		fmt.Printf("    %s [SIMULATION] Placed both sides: %s GREEN + RED @ %s interval, openTime=%d, amount=%.2f each\n",
			crYellow("→"), assetAddress.Hex()[:10], contracts.CandleRushInterval(intervalSeconds).String(), openTime, amount)
		return greenResult, redResult, nil
	}

	// Check USDC balance
	balance, err := e.usdc.BalanceOf(ctx, w.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get USDC balance: %w", err)
	}

	if balance.Cmp(totalNeeded) < 0 {
		return nil, nil, fmt.Errorf("insufficient USDC balance: have %s, need %s (2x%.2f)",
			balance.String(), totalNeeded.String(), amount)
	}

	// Ensure approval
	if err := e.ensureApproval(ctx, w, totalNeeded); err != nil {
		return nil, nil, fmt.Errorf("failed to ensure approval: %w", err)
	}

	// Prepare batch inputs for both sides
	inputs := []contracts.CandleRushBetInput{
		{
			Recipient:       w.Address,
			Asset:           assetAddress,
			IntervalSeconds: intervalSeconds,
			OpenTime:        openTime,
			Side:            uint8(contracts.CandleRushSideUp), // GREEN
			TokenIn:         e.usdcAddress,
			AmountIn:        new(big.Int).Set(amountBase),
			Broker:          uint32(e.brokerID),
		},
		{
			Recipient:       w.Address,
			Asset:           assetAddress,
			IntervalSeconds: intervalSeconds,
			OpenTime:        openTime,
			Side:            uint8(contracts.CandleRushSideDown), // RED
			TokenIn:         e.usdcAddress,
			AmountIn:        new(big.Int).Set(amountBase),
			Broker:          uint32(e.brokerID),
		},
	}

	fmt.Printf("    %s Placing both sides: %s GREEN + RED @ %s interval, openTime=%d, amount=%.2f each\n",
		crCyan("→"), assetAddress.Hex()[:10], contracts.CandleRushInterval(intervalSeconds).String(), openTime, amount)

	opts, err := w.GetTransactOpts(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get tx opts: %w", err)
	}
	opts.GasLimit = 1500000 // Higher gas for batch transaction

	// Place batch bet
	tx, err := e.candleRush.BatchPlaceBet(ctx, opts, inputs)
	if err != nil {
		w.ResetNonce()
		greenResult.Error = err
		redResult.Error = err
		return greenResult, redResult, fmt.Errorf("failed to place batch bet: %w", err)
	}

	txHash := tx.Hash().Hex()
	greenResult.TxHash = txHash
	redResult.TxHash = txHash

	fmt.Printf("    %s Batch bet TX: %s\n", crCyan("→"), txHash)

	// Wait for confirmation
	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		greenResult.Error = err
		redResult.Error = err
		return greenResult, redResult, fmt.Errorf("failed to wait for tx: %w", err)
	}

	if receipt.Status == 0 {
		err := fmt.Errorf("batch bet transaction failed")
		greenResult.Error = err
		redResult.Error = err
		return greenResult, redResult, err
	}

	greenResult.Confirmed = true
	redResult.Confirmed = true

	fmt.Printf("    %s Both sides confirmed in block %d: GREEN + RED @ %s, openTime=%d\n",
		crGreen("✓"), receipt.BlockNumber.Uint64(), contracts.CandleRushInterval(intervalSeconds).String(), openTime)

	return greenResult, redResult, nil
}

// ensureApproval ensures the USDC allowance is sufficient
func (e *Executor) ensureApproval(ctx context.Context, w *wallet.Wallet, amount *big.Int) error {
	allowance, err := e.usdc.Allowance(ctx, w.Address, e.diamondAddress)
	if err != nil {
		return fmt.Errorf("failed to get allowance: %w", err)
	}

	if allowance.Cmp(amount) >= 0 {
		return nil
	}

	fmt.Printf("    %s Approving USDC spending for %s...\n", crYellow("→"), w.Address.Hex()[:10])

	opts, err := w.GetTransactOpts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tx opts: %w", err)
	}

	// Approve max uint256 for convenience — Diamond contract is trusted
	tx, err := e.usdc.Approve(ctx, opts, e.diamondAddress, contracts.MaxUint256())
	if err != nil {
		w.ResetNonce()
		return fmt.Errorf("failed to approve: %w", err)
	}

	fmt.Printf("    %s Approval TX: %s\n", crCyan("→"), tx.Hash().Hex())

	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		return fmt.Errorf("failed to wait for approval: %w", err)
	}

	if receipt.Status == 0 {
		return fmt.Errorf("approval transaction failed")
	}

	fmt.Printf("    %s Approval confirmed in block %d\n", crGreen("✓"), receipt.BlockNumber.Uint64())
	return nil
}

// PlaceAllAssetsBothSides places bets on ALL provided assets, both GREEN and RED sides
// This creates 2 bets per asset (GREEN + RED) in a single batch transaction
func (e *Executor) PlaceAllAssetsBothSides(
	ctx context.Context,
	w *wallet.Wallet,
	assetAddresses []common.Address,
	intervalSeconds uint32,
	amountPerSide float64,
) (results []*BetResult, err error) {
	if len(assetAddresses) == 0 {
		return nil, fmt.Errorf("no assets provided")
	}

	// Calculate the next open time for this interval
	openTime := CalculateNextOpenTime(intervalSeconds)

	// Check if we have enough time before the candle opens
	now := time.Now().UTC().Unix()
	timeUntilOpen := int64(openTime) - now
	if timeUntilOpen < 10 {
		// Less than 10 seconds, skip to next candle
		openTime = openTime + uint64(intervalSeconds)
		fmt.Printf("    %s Too close to candle open, using next candle at %d\n", crYellow("!"), openTime)
	}

	amountBase := wallet.ToBaseUnits(amountPerSide, 6)
	// Total needed: 2 bets (GREEN + RED) per asset
	totalBets := len(assetAddresses) * 2
	totalNeeded := new(big.Int).Mul(amountBase, big.NewInt(int64(totalBets)))

	// Initialize results slice
	results = make([]*BetResult, 0, totalBets)

	if e.SimulationMode {
		for _, asset := range assetAddresses {
			greenResult := &BetResult{
				TxHash:    fmt.Sprintf("0xSIM_GREEN_%s", asset.Hex()[:8]),
				Side:      contracts.CandleRushSideUp,
				Interval:  contracts.CandleRushInterval(intervalSeconds),
				OpenTime:  openTime,
				Amount:    amountPerSide,
				Confirmed: true,
			}
			redResult := &BetResult{
				TxHash:    fmt.Sprintf("0xSIM_RED_%s", asset.Hex()[:8]),
				Side:      contracts.CandleRushSideDown,
				Interval:  contracts.CandleRushInterval(intervalSeconds),
				OpenTime:  openTime,
				Amount:    amountPerSide,
				Confirmed: true,
			}
			results = append(results, greenResult, redResult)
		}
		fmt.Printf("    %s [SIMULATION] Placed %d bets on %d assets @ %s interval, openTime=%d, amount=%.2f each\n",
			crYellow("→"), totalBets, len(assetAddresses), contracts.CandleRushInterval(intervalSeconds).String(), openTime, amountPerSide)
		return results, nil
	}

	// Check USDC balance
	balance, err := e.usdc.BalanceOf(ctx, w.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to get USDC balance: %w", err)
	}

	if balance.Cmp(totalNeeded) < 0 {
		return nil, fmt.Errorf("insufficient USDC balance: have %s, need %s (%dx%.2f for %d assets)",
			balance.String(), totalNeeded.String(), totalBets, amountPerSide, len(assetAddresses))
	}

	// Ensure approval
	if err := e.ensureApproval(ctx, w, totalNeeded); err != nil {
		return nil, fmt.Errorf("failed to ensure approval: %w", err)
	}

	// Prepare batch inputs for all assets, both sides
	inputs := make([]contracts.CandleRushBetInput, 0, totalBets)

	for _, asset := range assetAddresses {
		// GREEN side
		inputs = append(inputs, contracts.CandleRushBetInput{
			Recipient:       w.Address,
			Asset:           asset,
			IntervalSeconds: intervalSeconds,
			OpenTime:        openTime,
			Side:            uint8(contracts.CandleRushSideUp), // GREEN
			TokenIn:         e.usdcAddress,
			AmountIn:        new(big.Int).Set(amountBase),
			Broker:          uint32(e.brokerID),
		})
		// RED side
		inputs = append(inputs, contracts.CandleRushBetInput{
			Recipient:       w.Address,
			Asset:           asset,
			IntervalSeconds: intervalSeconds,
			OpenTime:        openTime,
			Side:            uint8(contracts.CandleRushSideDown), // RED
			TokenIn:         e.usdcAddress,
			AmountIn:        new(big.Int).Set(amountBase),
			Broker:          uint32(e.brokerID),
		})

		// Initialize result entries
		results = append(results, &BetResult{
			Side:     contracts.CandleRushSideUp,
			Interval: contracts.CandleRushInterval(intervalSeconds),
			OpenTime: openTime,
			Amount:   amountPerSide,
		})
		results = append(results, &BetResult{
			Side:     contracts.CandleRushSideDown,
			Interval: contracts.CandleRushInterval(intervalSeconds),
			OpenTime: openTime,
			Amount:   amountPerSide,
		})
	}

	fmt.Printf("    %s Placing %d bets on %d assets: GREEN + RED @ %s interval, openTime=%d, amount=%.2f each\n",
		crCyan("→"), totalBets, len(assetAddresses), contracts.CandleRushInterval(intervalSeconds).String(), openTime, amountPerSide)

	opts, err := w.GetTransactOpts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get tx opts: %w", err)
	}
	opts.GasLimit = uint64(500000 + 300000*len(assetAddresses)) // Base gas + per-asset gas

	// Place batch bet
	tx, err := e.candleRush.BatchPlaceBet(ctx, opts, inputs)
	if err != nil {
		w.ResetNonce()
		for i := range results {
			results[i].Error = err
		}
		return results, fmt.Errorf("failed to place batch bet: %w", err)
	}

	txHash := tx.Hash().Hex()
	for i := range results {
		results[i].TxHash = txHash
	}

	fmt.Printf("    %s Batch bet TX: %s %s\n", crCyan("→"), txHash, crDim(fmt.Sprintf("(%d bets)", totalBets)))

	// Wait for confirmation
	ctxTimeout, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		for i := range results {
			results[i].Error = err
		}
		return results, fmt.Errorf("failed to wait for tx: %w", err)
	}

	if receipt.Status == 0 {
		err := fmt.Errorf("batch bet transaction failed")
		for i := range results {
			results[i].Error = err
		}
		return results, err
	}

	for i := range results {
		results[i].Confirmed = true
	}

	fmt.Printf("    %s All %d bets confirmed in block %d @ %s, openTime=%d\n",
		crGreen("✓"), totalBets, receipt.BlockNumber.Uint64(), contracts.CandleRushInterval(intervalSeconds).String(), openTime)

	return results, nil
}

// PlaceAllAssetsMultiCandle places bets on ALL assets for multiple consecutive candles
// in a single batch transaction. For each candle openTime, it places GREEN + RED on every asset.
// Total bets = len(assets) * numCandles * 2 (GREEN + RED)
// lastBetOpenTime: the last candle open time we bet on for this interval (0 if first round).
// The method ensures all new bets are on candles strictly after lastBetOpenTime.
func (e *Executor) PlaceAllAssetsMultiCandle(
	ctx context.Context,
	w *wallet.Wallet,
	assetAddresses []common.Address,
	intervalSeconds uint32,
	numCandles int,
	amountPerSide float64,
	lastBetOpenTime uint64,
) (results []*BetResult, err error) {
	if len(assetAddresses) == 0 {
		return nil, fmt.Errorf("no assets provided")
	}
	if numCandles <= 0 {
		return nil, fmt.Errorf("numCandles must be > 0")
	}

	// Calculate the first open time, ensuring it's after the last bet
	var firstOpenTime uint64
	if lastBetOpenTime > 0 {
		firstOpenTime = CalculateNextOpenTimeAfter(intervalSeconds, lastBetOpenTime)
	} else {
		firstOpenTime = CalculateNextOpenTime(intervalSeconds)
	}

	// Check if we have enough time before the candle opens
	now := time.Now().UTC().Unix()
	timeUntilOpen := int64(firstOpenTime) - now
	if timeUntilOpen < 10 {
		firstOpenTime = firstOpenTime + uint64(intervalSeconds)
		fmt.Printf("    %s Too close to candle open, using next candle at %d\n", crYellow("!"), firstOpenTime)
	}

	// Calculate all candle open times (consecutive candles)
	openTimes := make([]uint64, numCandles)
	for i := 0; i < numCandles; i++ {
		openTimes[i] = firstOpenTime + uint64(i)*uint64(intervalSeconds)
	}

	amountBase := wallet.ToBaseUnits(amountPerSide, 6)
	totalBets := len(assetAddresses) * numCandles * 2 // 2 sides per asset per candle
	totalNeeded := new(big.Int).Mul(amountBase, big.NewInt(int64(totalBets)))

	results = make([]*BetResult, 0, totalBets)

	if e.SimulationMode {
		for _, ot := range openTimes {
			for _, asset := range assetAddresses {
				results = append(results, &BetResult{
					TxHash:    fmt.Sprintf("0xSIM_GREEN_%s_%d", asset.Hex()[:8], ot),
					Side:      contracts.CandleRushSideUp,
					Interval:  contracts.CandleRushInterval(intervalSeconds),
					OpenTime:  ot,
					Amount:    amountPerSide,
					Confirmed: true,
				})
				results = append(results, &BetResult{
					TxHash:    fmt.Sprintf("0xSIM_RED_%s_%d", asset.Hex()[:8], ot),
					Side:      contracts.CandleRushSideDown,
					Interval:  contracts.CandleRushInterval(intervalSeconds),
					OpenTime:  ot,
					Amount:    amountPerSide,
					Confirmed: true,
				})
			}
		}
		fmt.Printf("    %s [SIMULATION] Placed %d bets: %d assets x %d candles x 2 sides @ %s interval, amount=%.2f each\n",
			crYellow("→"), totalBets, len(assetAddresses), numCandles, contracts.CandleRushInterval(intervalSeconds).String(), amountPerSide)
		return results, nil
	}

	// Check USDC balance
	balance, err := e.usdc.BalanceOf(ctx, w.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to get USDC balance: %w", err)
	}

	if balance.Cmp(totalNeeded) < 0 {
		return nil, fmt.Errorf("insufficient USDC balance: have %s, need %s (%d bets × %.2f for %d assets × %d candles)",
			balance.String(), totalNeeded.String(), totalBets, amountPerSide, len(assetAddresses), numCandles)
	}

	// Ensure approval
	if err := e.ensureApproval(ctx, w, totalNeeded); err != nil {
		return nil, fmt.Errorf("failed to ensure approval: %w", err)
	}

	// Prepare batch inputs: for each candle open time, for each asset, GREEN + RED
	inputs := make([]contracts.CandleRushBetInput, 0, totalBets)

	for _, ot := range openTimes {
		for _, asset := range assetAddresses {
			// GREEN side
			inputs = append(inputs, contracts.CandleRushBetInput{
				Recipient:       w.Address,
				Asset:           asset,
				IntervalSeconds: intervalSeconds,
				OpenTime:        ot,
				Side:            uint8(contracts.CandleRushSideUp),
				TokenIn:         e.usdcAddress,
				AmountIn:        new(big.Int).Set(amountBase),
				Broker:          uint32(e.brokerID),
			})
			// RED side
			inputs = append(inputs, contracts.CandleRushBetInput{
				Recipient:       w.Address,
				Asset:           asset,
				IntervalSeconds: intervalSeconds,
				OpenTime:        ot,
				Side:            uint8(contracts.CandleRushSideDown),
				TokenIn:         e.usdcAddress,
				AmountIn:        new(big.Int).Set(amountBase),
				Broker:          uint32(e.brokerID),
			})

			// Initialize result entries
			results = append(results, &BetResult{
				Side:     contracts.CandleRushSideUp,
				Interval: contracts.CandleRushInterval(intervalSeconds),
				OpenTime: ot,
				Amount:   amountPerSide,
			})
			results = append(results, &BetResult{
				Side:     contracts.CandleRushSideDown,
				Interval: contracts.CandleRushInterval(intervalSeconds),
				OpenTime: ot,
				Amount:   amountPerSide,
			})
		}
	}

	fmt.Printf("    %s Placing %d bets: %d assets x %d candles x 2 sides @ %s interval, amount=%.2f each\n",
		crCyan("→"), totalBets, len(assetAddresses), numCandles, contracts.CandleRushInterval(intervalSeconds).String(), amountPerSide)

	opts, err := w.GetTransactOpts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get tx opts: %w", err)
	}
	// Gas: base + per-bet gas (each bet is an asset×candle×side combo)
	opts.GasLimit = uint64(500000 + 200000*totalBets)

	// Place batch bet
	tx, err := e.candleRush.BatchPlaceBet(ctx, opts, inputs)
	if err != nil {
		w.ResetNonce()
		for i := range results {
			results[i].Error = err
		}
		return results, fmt.Errorf("failed to place batch bet: %w", err)
	}

	txHash := tx.Hash().Hex()
	for i := range results {
		results[i].TxHash = txHash
	}

	fmt.Printf("    %s Multi-candle batch TX: %s %s\n", crCyan("→"), txHash, crDim(fmt.Sprintf("(%d bets)", totalBets)))

	// Wait for confirmation (longer timeout for large batch)
	ctxTimeout, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		for i := range results {
			results[i].Error = err
		}
		return results, fmt.Errorf("failed to wait for tx: %w", err)
	}

	if receipt.Status == 0 {
		err := fmt.Errorf("batch bet transaction failed")
		for i := range results {
			results[i].Error = err
		}
		return results, err
	}

	for i := range results {
		results[i].Confirmed = true
	}

	fmt.Printf("    %s All %d bets confirmed in block %d @ %s, candles=%d\n",
		crGreen("✓"), totalBets, receipt.BlockNumber.Uint64(), contracts.CandleRushInterval(intervalSeconds).String(), numCandles)

	return results, nil
}

// CheckBalance checks USDC balance for an address
func (e *Executor) CheckBalance(ctx context.Context, address common.Address) (float64, error) {
	if e.SimulationMode {
		return 1000.0, nil
	}
	balance, err := e.usdc.BalanceOf(ctx, address)
	if err != nil {
		return 0, err
	}
	return wallet.FromBaseUnits(balance, 6), nil
}

// FormatOpenTime formats a unix timestamp for display
func FormatOpenTime(openTime uint64) string {
	t := time.Unix(int64(openTime), 0).UTC()
	return t.Format("15:04:05 UTC")
}
