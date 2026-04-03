package betting

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/fatih/color"

	"github.com/blinq-fi/blinq-mm-bot/internal/contracts"
	"github.com/blinq-fi/blinq-mm-bot/internal/prices"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

var (
	greenText  = color.New(color.FgGreen).SprintFunc()
	yellowText = color.New(color.FgYellow).SprintFunc()
	cyanText   = color.New(color.FgCyan).SprintFunc()
	redText    = color.New(color.FgRed).SprintFunc()
	dimText    = color.New(color.Faint).SprintFunc()
)

// Executor handles bet placement
type Executor struct {
	diamond        *contracts.Diamond
	usdc           *contracts.ERC20
	priceFetcher   *prices.Fetcher
	usdcAddress    common.Address
	diamondAddress common.Address
	toleranceBps   int
	brokerID       int
	maxPriceAge    time.Duration
	priceRetries   int
	SimulationMode bool
}

// NewExecutor creates a new bet executor
func NewExecutor(
	diamond *contracts.Diamond,
	usdc *contracts.ERC20,
	priceFetcher *prices.Fetcher,
	usdcAddress, diamondAddress common.Address,
	toleranceBps, brokerID int,
) *Executor {
	return &Executor{
		diamond:        diamond,
		usdc:           usdc,
		priceFetcher:   priceFetcher,
		usdcAddress:    usdcAddress,
		diamondAddress: diamondAddress,
		toleranceBps:   toleranceBps,
		brokerID:       brokerID,
		maxPriceAge:    prices.DefaultMaxPriceAge,
		priceRetries:   3,
	}
}

// NewExecutorWithOptions creates a new bet executor with additional options
func NewExecutorWithOptions(
	diamond *contracts.Diamond,
	usdc *contracts.ERC20,
	priceFetcher *prices.Fetcher,
	usdcAddress, diamondAddress common.Address,
	toleranceBps, brokerID int,
	maxPriceAge time.Duration,
	priceRetries int,
) *Executor {
	return &Executor{
		diamond:        diamond,
		usdc:           usdc,
		priceFetcher:   priceFetcher,
		usdcAddress:    usdcAddress,
		diamondAddress: diamondAddress,
		toleranceBps:   toleranceBps,
		brokerID:       brokerID,
		maxPriceAge:    maxPriceAge,
		priceRetries:   priceRetries,
	}
}

// ExecuteBet places a bet based on the selection
func (e *Executor) ExecuteBet(ctx context.Context, w *wallet.Wallet, bet *BetSelection) (string, error) {
	// 1. Check USDC balance
	if e.SimulationMode {
		// Mock sufficient balance
		return "0xSIMULATED_TX_HASH", nil
	}

	balance, err := e.usdc.BalanceOf(ctx, w.Address)
	if err != nil {
		return "", fmt.Errorf("failed to get USDC balance: %w", err)
	}

	amountBase := wallet.ToBaseUnits(bet.Amount, 6)
	if balance.Cmp(amountBase) < 0 {
		return "", fmt.Errorf("insufficient USDC balance: have %s, need %s",
			balance.String(), amountBase.String())
	}

	// 2. Check and approve USDC if needed
	if err := e.ensureApproval(ctx, w, amountBase); err != nil {
		return "", fmt.Errorf("failed to ensure approval: %w", err)
	}

	// 3. Fetch FRESH prices right before execution (with freshness validation and retries)
	priceData, err := e.priceFetcher.FetchFreshPrices(ctx, bet.PriceIDs, e.maxPriceAge, e.priceRetries)
	if err != nil {
		return "", fmt.Errorf("failed to fetch fresh prices: %w", err)
	}

	// Log price ages for debugging
	for id, data := range priceData {
		age := prices.GetPriceAge(data)
		fmt.Printf("  %s Price %s age: %s\n", dimText("·"), id[:8], cyanText(age.Round(time.Millisecond).String()))
	}

	// 4. Execute based on bet type
	var txHash string
	if bet.Type == BetTypeUpDown {
		txHash, err = e.executeUpDownBet(ctx, w, bet, priceData)
	} else {
		txHash, err = e.executeRelativeBet(ctx, w, bet, priceData)
	}

	if err != nil {
		return "", err
	}

	return txHash, nil
}

func (e *Executor) ensureApproval(ctx context.Context, w *wallet.Wallet, amount *big.Int) error {
	allowance, err := e.usdc.Allowance(ctx, w.Address, e.diamondAddress)
	if err != nil {
		return fmt.Errorf("failed to get allowance: %w", err)
	}

	if allowance.Cmp(amount) >= 0 {
		return nil
	}

	fmt.Printf("  %s Approving USDC spending for %s...\n", yellowText("→"), w.Address.Hex()[:10])

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

	fmt.Printf("  %s Approval TX: %s\n", cyanText("→"), tx.Hash().Hex())

	// Wait for confirmation with timeout
	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		return fmt.Errorf("failed to wait for approval: %w", err)
	}

	if receipt.Status == 0 {
		return fmt.Errorf("approval transaction failed")
	}

	fmt.Printf("  %s Approval confirmed in block %d\n", greenText("✓"), receipt.BlockNumber.Uint64())
	return nil
}

func (e *Executor) executeUpDownBet(
	ctx context.Context,
	w *wallet.Wallet,
	bet *BetSelection,
	priceData map[string]*prices.PythPriceData,
) (string, error) {
	// Get price for the asset
	priceID := bet.PriceIDs[0]
	data, ok := priceData[priceID]
	if !ok {
		return "", fmt.Errorf("price not found for %s", priceID)
	}

	// Convert price to 1e8 format
	price1e8, err := prices.ConvertPythPriceTo1e8(data.Price.Price, data.Price.Expo)
	if err != nil {
		return "", fmt.Errorf("failed to convert price: %w", err)
	}

	// Calculate acceptable price with tolerance
	acceptablePrice := prices.CalculateAcceptablePrice(price1e8, e.toleranceBps, bet.Direction)

	// Get pair address
	pairAddress := common.HexToAddress(*bet.Market.Base)

	// Parse period from string to int
	periodInt, err := strconv.Atoi(bet.Period.Period)
	if err != nil {
		return "", fmt.Errorf("failed to parse period %s: %w", bet.Period.Period, err)
	}

	brokerValue := big.NewInt(int64(e.brokerID))

	// Build params
	params := contracts.PredictAndBetParams{
		Recipient:          w.Address,
		PredictionPairBase: pairAddress,
		IsUp:               bet.Direction,
		Period:             uint8(periodInt),
		TokenIn:            e.usdcAddress,
		AmountIn:           wallet.ToBaseUnits(bet.Amount, 6),
		Price:              acceptablePrice,
		Broker:             brokerValue,
	}

	fmt.Printf("  %s Placing UP/DOWN bet: %s %s at price %d %s\n",
		cyanText("→"), bet.AssetSymbol, bet.GetDirectionString(), price1e8,
		dimText(fmt.Sprintf("(acceptable: %d)", acceptablePrice)))

	opts, err := w.GetTransactOpts(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get tx opts: %w", err)
	}

	// Add gas limit buffer (increased for complex contract calls)
	opts.GasLimit = 1000000

	tx, err := e.diamond.PredictAndBet(ctx, opts, params)
	if err != nil {
		w.ResetNonce()
		return "", fmt.Errorf("failed to place bet: %w", err)
	}

	fmt.Printf("  %s UP/DOWN bet TX: %s\n", cyanText("→"), tx.Hash().Hex())

	// Wait for confirmation with timeout
	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		return tx.Hash().Hex(), fmt.Errorf("failed to wait for tx: %w", err)
	}

	if receipt.Status == 0 {
		return tx.Hash().Hex(), fmt.Errorf("bet transaction failed")
	}

	fmt.Printf("  %s UP/DOWN bet confirmed in block %d\n", greenText("✓"), receipt.BlockNumber.Uint64())
	return tx.Hash().Hex(), nil
}

func (e *Executor) executeRelativeBet(
	ctx context.Context,
	w *wallet.Wallet,
	bet *BetSelection,
	priceData map[string]*prices.PythPriceData,
) (string, error) {
	// Get prices for all assets
	var currentPrices []uint64
	for _, priceID := range bet.PriceIDs {
		data, ok := priceData[priceID]
		if !ok {
			return "", fmt.Errorf("price not found for %s", priceID)
		}

		price1e8, err := prices.ConvertPythPriceTo1e8(data.Price.Price, data.Price.Expo)
		if err != nil {
			return "", fmt.Errorf("failed to convert price: %w", err)
		}
		currentPrices = append(currentPrices, price1e8)
	}

	// Parse period from string to int
	periodInt, err := strconv.Atoi(bet.Period.Period)
	if err != nil {
		return "", fmt.Errorf("failed to parse period %s: %w", bet.Period.Period, err)
	}

	brokerValue := big.NewInt(int64(e.brokerID))

	// Calculate acceptable entry prices
	acceptablePrices := prices.CalculateAcceptableEntryPrices(currentPrices, e.toleranceBps, bet.RuleType)

	// Build asset addresses
	var assets []common.Address
	for _, addr := range bet.Market.Assets {
		assets = append(assets, common.HexToAddress(addr))
	}

	// Build params
	params := contracts.PredictRelativeAndBetParams{
		Recipient:             w.Address,
		RuleType:              bet.RuleType,
		Period:                uint8(periodInt),
		SideIndex:             bet.SideIndex,
		TokenIn:               e.usdcAddress,
		AmountIn:              wallet.ToBaseUnits(bet.Amount, 6),
		Broker:                brokerValue,
		Assets:                assets,
		AcceptableEntryPrices: acceptablePrices,
	}

	var priceStrs []string
	for _, p := range currentPrices {
		priceStrs = append(priceStrs, fmt.Sprintf("%d", p))
	}

	fmt.Printf("  %s Placing RELATIVE bet: %s %s on %s at prices [%s]\n",
		cyanText("→"), bet.GetMarketDescription(), bet.GetDirectionString(), bet.AssetSymbol,
		strings.Join(priceStrs, ", "))

	opts, err := w.GetTransactOpts(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get tx opts: %w", err)
	}

	// Add gas limit buffer (increased for complex contract calls)
	opts.GasLimit = 1000000

	tx, err := e.diamond.PredictRelativeAndBet(ctx, opts, params)
	if err != nil {
		w.ResetNonce()
		return "", fmt.Errorf("failed to place bet: %w", err)
	}

	fmt.Printf("  %s RELATIVE bet TX: %s\n", cyanText("→"), tx.Hash().Hex())

	// Wait for confirmation with timeout
	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	receipt, err := w.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		return tx.Hash().Hex(), fmt.Errorf("failed to wait for tx: %w", err)
	}

	if receipt.Status == 0 {
		return tx.Hash().Hex(), fmt.Errorf("bet transaction failed")
	}

	fmt.Printf("  %s RELATIVE bet confirmed in block %d\n", greenText("✓"), receipt.BlockNumber.Uint64())
	return tx.Hash().Hex(), nil
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

// FundWallet transfers USDC from owner to betting wallet
func (e *Executor) FundWallet(ctx context.Context, owner *wallet.Wallet, to common.Address, amount float64) error {
	if e.SimulationMode {
		fmt.Printf("  %s [SIMULATION] Funded wallet %s with %.2f USDC\n", yellowText("→"), to.Hex()[:10], amount)
		return nil
	}
	amountBase := wallet.ToBaseUnits(amount, 6)

	// Check owner balance
	balance, err := e.usdc.BalanceOf(ctx, owner.Address)
	if err != nil {
		return fmt.Errorf("failed to get owner balance: %w", err)
	}

	if balance.Cmp(amountBase) < 0 {
		return fmt.Errorf("owner has insufficient USDC: have %s, need %s",
			balance.String(), amountBase.String())
	}

	opts, err := owner.GetTransactOpts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tx opts: %w", err)
	}

	tx, err := e.usdc.Transfer(ctx, opts, to, amountBase)
	if err != nil {
		owner.ResetNonce()
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	fmt.Printf("  %s USDC funding TX: %s → %s (%.2f USDC)\n", cyanText("→"), owner.Address.Hex()[:10], to.Hex()[:10], amount)

	ctxTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	receipt, err := owner.WaitForTx(ctxTimeout, tx.Hash())
	if err != nil {
		return fmt.Errorf("failed to wait for transfer: %w", err)
	}

	if receipt.Status == 0 {
		return fmt.Errorf("transfer transaction failed")
	}

	fmt.Printf("  %s USDC funding confirmed: %.2f USDC to %s\n", greenText("✓"), amount, to.Hex()[:10])
	return nil
}

// CheckNativeBalance checks native MON balance for an address
func (e *Executor) CheckNativeBalance(ctx context.Context, w *wallet.Wallet, address common.Address) (float64, error) {
	if e.SimulationMode {
		return 10.0, nil
	}
	balance, err := w.GetBalance(ctx, address)
	if err != nil {
		return 0, err
	}
	return wallet.FromWei(balance), nil
}

// FundGas transfers native MON from owner to betting wallet for gas
func (e *Executor) FundGas(ctx context.Context, owner *wallet.Wallet, to common.Address, amount float64) error {
	if e.SimulationMode {
		fmt.Printf("  %s [SIMULATION] Funded gas for %s with %.4f MON\n", yellowText("→"), to.Hex()[:10], amount)
		return nil
	}
	amountWei := wallet.ToWei(amount)

	// Check owner native balance
	ownerBalance, err := owner.GetBalance(ctx, owner.Address)
	if err != nil {
		return fmt.Errorf("failed to get owner native balance: %w", err)
	}

	if ownerBalance.Cmp(amountWei) < 0 {
		return fmt.Errorf("owner has insufficient MON: have %.4f, need %.4f",
			wallet.FromWei(ownerBalance), amount)
	}

	txHash, err := owner.SendNative(ctx, to, amountWei)
	if err != nil {
		return fmt.Errorf("failed to send MON: %w", err)
	}

	fmt.Printf("  %s Gas funding TX: %s → %s (%.4f MON) %s\n",
		cyanText("→"), owner.Address.Hex()[:10], to.Hex()[:10], amount, dimText(txHash.Hex()[:16]+"..."))

	receipt, err := owner.WaitForTx(ctx, txHash)
	if err != nil {
		return fmt.Errorf("failed to wait for gas transfer: %w", err)
	}

	if receipt.Status == 0 {
		return fmt.Errorf("gas transfer transaction failed")
	}

	fmt.Printf("  %s Gas funding confirmed: %.4f MON to %s\n", greenText("✓"), amount, to.Hex()[:10])
	return nil
}
