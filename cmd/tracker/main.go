package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/fatih/color"
)

var (
	diamondAddress    = common.HexToAddress("0x928dc8afe312df45576b15b08c086c5427fd8207")
	candleRushAddress = common.HexToAddress("0x8EC30B1b03e6bB529149fF89aA1F9a9BaB2CB26a")
	usdcAddress       = common.HexToAddress("0x754704Bc059F8C67012fEd69BC8A327a5aafb603")

	assetSymbols = map[common.Address]string{
		common.HexToAddress("0x0555E30da8f98308EdB960aa94C0Db47230d2B9c"): "BTC",
		common.HexToAddress("0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242"): "ETH",
		common.HexToAddress("0xea17E5a9efEBf1477dB45082d67010E2245217f1"): "SOL",
	}

	periodTimeframes = map[uint8]string{
		0: "1m", 1: "5m", 2: "10m", 3: "15m", 4: "30m", 5: "1h",
	}

	predictAndBetPendingTopic   = crypto.Keccak256Hash([]byte("PredictAndBetPending(address,uint256,(address,uint96,address,uint96,address,uint64,uint24,bool,uint128,uint8))"))
	predictRelativePendingTopic = crypto.Keccak256Hash([]byte("PredictRelativePending(address,uint256,(address,uint96,uint96,address,bytes32,uint8,uint8,uint8,uint24,uint128,uint64[]))"))
	transferTopic               = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

const requestTimeout = 3 * time.Minute

func main() {
	wallet := "0xE538e578f4D1195EF3F7E434f4bff3c62F8FfB24"
	if len(os.Args) > 1 {
		wallet = os.Args[1]
	}

	walletAddr := common.HexToAddress(wallet)

	rpcURL := os.Getenv("RPC_URL")
	if rpcURL == "" {
		rpcURL = "https://rpc.monad.xyz"
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()

	fmt.Printf("\n%s\n", bold("===================================="))
	fmt.Printf("%s Wallet: %s\n", bold("BLINQ BET TRACKER"), cyan(wallet))
	fmt.Printf("%s\n\n", bold("===================================="))

	// --- Current block ---
	blockNum, err := client.BlockNumber(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get block: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Current block: %s\n", cyan(blockNum))

	// --- Nonce ---
	nonce, err := client.NonceAt(ctx, walletAddr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get nonce: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Total txs sent: %s\n", cyan(nonce))

	// --- Balances ---
	fmt.Printf("\n%s\n", bold("=== BALANCES ==="))
	monBalance, err := client.BalanceAt(ctx, walletAddr, nil)
	if err == nil {
		monFloat := new(big.Float).Quo(new(big.Float).SetInt(monBalance), new(big.Float).SetFloat64(1e18))
		fmt.Printf("  MON:  %s\n", green(monFloat.Text('f', 6)))
	}
	usdcBalance, err := getERC20Balance(ctx, client, usdcAddress, walletAddr)
	if err == nil {
		usdcFloat := float64(usdcBalance.Int64()) / 1e6
		fmt.Printf("  USDC: %s\n", green(fmt.Sprintf("%.6f", usdcFloat)))
	}

	// --- Find active block range via binary search ---
	fmt.Printf("\n%s\n", bold("=== FINDING WALLET ACTIVITY ==="))
	fmt.Printf("  %s Binary searching for first activity...\n", dim("~"))

	activeFrom, activeTo, err := findActivityRange(ctx, client, walletAddr, blockNum)
	if err != nil {
		fmt.Printf("  %s Could not find activity range: %v\n", red("!"), err)
		fmt.Printf("  %s Falling back to recent blocks...\n", yellow("~"))
		// Fallback: scan last N blocks
		fallback := uint64(5000)
		if lb := os.Getenv("LOOKBACK"); lb != "" {
			if v, e := strconv.ParseUint(lb, 10, 64); e == nil {
				fallback = v
			}
		}
		activeFrom = blockNum - fallback
		activeTo = blockNum
	} else {
		fmt.Printf("  %s Wallet active between blocks %s and %s\n",
			green("OK"), cyan(activeFrom), cyan(activeTo))
	}

	// Add some padding
	if activeFrom > 500 {
		activeFrom -= 500
	} else {
		activeFrom = 0
	}
	activeTo = blockNum // always scan to current

	scanRange := activeTo - activeFrom
	fmt.Printf("  Scanning %s blocks (from %d to %d)\n", cyan(scanRange), activeFrom, activeTo)

	userTopic := common.BytesToHash(walletAddr.Bytes())
	fromTopic := common.BytesToHash(walletAddr.Bytes())
	toWalletTopic := common.BytesToHash(walletAddr.Bytes())

	// ===== ALL USDC TRANSFERS INVOLVING THIS WALLET =====
	// This catches everything: bets, claims, funding, etc.
	fmt.Printf("\n%s\n", bold("=== ALL USDC TRANSFERS (incoming) ==="))
	incomingLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {}, {toWalletTopic}},
	)
	if len(incomingLogs) == 0 {
		fmt.Printf("  No incoming USDC transfers found\n")
	} else {
		totalIn := 0.0
		for _, log := range incomingLogs {
			amount := new(big.Int).SetBytes(log.Data)
			amountFloat := float64(amount.Int64()) / 1e6
			totalIn += amountFloat
			from := common.BytesToAddress(log.Topics[1].Bytes())
			label := resolveContract(from)
			fmt.Printf("  Block %d | FROM %s | +%s USDC | TX %s\n",
				log.BlockNumber, label,
				green(fmt.Sprintf("%.4f", amountFloat)),
				dim(log.TxHash.Hex()[:20]+"..."),
			)
		}
		fmt.Printf("  %s Total received: %s USDC\n", bold(">>"), green(fmt.Sprintf("%.4f", totalIn)))
	}

	fmt.Printf("\n%s\n", bold("=== ALL USDC TRANSFERS (outgoing) ==="))
	outgoingLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {fromTopic}, {}},
	)
	if len(outgoingLogs) == 0 {
		fmt.Printf("  No outgoing USDC transfers found\n")
	} else {
		totalOut := 0.0
		for _, log := range outgoingLogs {
			amount := new(big.Int).SetBytes(log.Data)
			amountFloat := float64(amount.Int64()) / 1e6
			totalOut += amountFloat
			to := common.BytesToAddress(log.Topics[2].Bytes())
			label := resolveContract(to)
			fmt.Printf("  Block %d | TO %s | -%s USDC | TX %s\n",
				log.BlockNumber, label,
				red(fmt.Sprintf("%.4f", amountFloat)),
				dim(log.TxHash.Hex()[:20]+"..."),
			)
		}
		fmt.Printf("  %s Total sent: %s USDC\n", bold(">>"), red(fmt.Sprintf("%.4f", totalOut)))
	}

	// ===== BET EVENTS =====
	fmt.Printf("\n%s\n", bold("=== UP/DOWN BET EVENTS ==="))
	upDownLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{diamondAddress},
		[][]common.Hash{{predictAndBetPendingTopic}, {userTopic}},
	)
	if len(upDownLogs) == 0 {
		fmt.Printf("  No UP/DOWN bets found\n")
	} else {
		fmt.Printf("  Found %s UP/DOWN bets:\n", yellow(len(upDownLogs)))
		for _, log := range upDownLogs {
			parsePredictAndBetLog(log, green, red, cyan)
		}
	}

	fmt.Printf("\n%s\n", bold("=== RELATIVE BET EVENTS ==="))
	relativeLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{diamondAddress},
		[][]common.Hash{{predictRelativePendingTopic}, {userTopic}},
	)
	if len(relativeLogs) == 0 {
		fmt.Printf("  No RELATIVE bets found\n")
	} else {
		fmt.Printf("  Found %s RELATIVE bets:\n", yellow(len(relativeLogs)))
		for _, log := range relativeLogs {
			parseRelativeBetLog(log, green, red, cyan)
		}
	}

	// ===== P&L SUMMARY =====
	fmt.Printf("\n%s\n", bold("========== P&L SUMMARY =========="))

	// Diamond flows
	diamondToTopic := common.BytesToHash(diamondAddress.Bytes())
	diamondFromTopic := common.BytesToHash(diamondAddress.Bytes())

	diamondBetLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {fromTopic}, {diamondToTopic}},
	)
	diamondClaimLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {diamondFromTopic}, {toWalletTopic}},
	)

	totalDiamondBet := sumTransfers(diamondBetLogs)
	totalDiamondClaim := sumTransfers(diamondClaimLogs)

	// CandleRush flows
	crToTopic := common.BytesToHash(candleRushAddress.Bytes())
	crFromTopic := common.BytesToHash(candleRushAddress.Bytes())

	crBetLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {fromTopic}, {crToTopic}},
	)
	crClaimLogs, _ := paginatedFilterLogs(ctx, client, activeFrom, activeTo,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {crFromTopic}, {toWalletTopic}},
	)

	totalCRBet := sumTransfers(crBetLogs)
	totalCRClaim := sumTransfers(crClaimLogs)

	totalBet := totalDiamondBet + totalCRBet
	totalClaimed := totalDiamondClaim + totalCRClaim
	pnl := totalClaimed - totalBet

	fmt.Printf("  Diamond    | Bet: %s | Won: %s | P&L: %s\n",
		yellow(fmt.Sprintf("%.4f", totalDiamondBet)),
		green(fmt.Sprintf("%.4f", totalDiamondClaim)),
		formatPnL(totalDiamondClaim-totalDiamondBet, green, red))
	fmt.Printf("  CandleRush | Bet: %s | Won: %s | P&L: %s\n",
		yellow(fmt.Sprintf("%.4f", totalCRBet)),
		green(fmt.Sprintf("%.4f", totalCRClaim)),
		formatPnL(totalCRClaim-totalCRBet, green, red))
	fmt.Printf("  %s\n", bold("─────────────────────────────────────────────"))
	fmt.Printf("  TOTAL      | Bet: %s | Won: %s | P&L: %s\n",
		yellow(fmt.Sprintf("%.4f", totalBet)),
		green(fmt.Sprintf("%.4f", totalClaimed)),
		formatPnL(pnl, green, red))

	fmt.Printf("\n  Current USDC balance: %s\n", green(fmt.Sprintf("%.4f", float64(usdcBalance.Int64())/1e6)))
	fmt.Println()
}

// findActivityRange uses binary search on nonce to find when the wallet first sent a tx.
// Then uses USDC balance binary search to find when it first received funds.
func findActivityRange(ctx context.Context, client *ethclient.Client, wallet common.Address, currentBlock uint64) (uint64, uint64, error) {
	// Check nonce at current block
	currentNonce, err := client.NonceAt(ctx, wallet, nil)
	if err != nil {
		return 0, 0, err
	}
	if currentNonce == 0 {
		// No transactions sent — check if wallet received anything
		// by binary searching USDC balance
		return findFirstUSDCReceived(ctx, client, wallet, currentBlock)
	}

	// Binary search: find the lowest block where nonce > 0
	lo := uint64(0)
	hi := currentBlock
	for lo < hi {
		mid := lo + (hi-lo)/2
		n, err := client.NonceAt(ctx, wallet, new(big.Int).SetUint64(mid))
		if err != nil {
			// If error, try to narrow from the other side
			lo = mid + 1
			continue
		}
		if n > 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	firstTxBlock := lo
	fmt.Printf("  %s First outgoing tx at block ~%d\n", color.New(color.FgCyan).Sprint("~"), firstTxBlock)

	// Also find first USDC activity (might be earlier — wallet gets funded first)
	usdcFrom, _, err := findFirstUSDCReceived(ctx, client, wallet, currentBlock)
	if err == nil && usdcFrom < firstTxBlock {
		firstTxBlock = usdcFrom
	}

	return firstTxBlock, currentBlock, nil
}

// findFirstUSDCReceived binary searches for when USDC balance first became > 0
func findFirstUSDCReceived(ctx context.Context, client *ethclient.Client, wallet common.Address, currentBlock uint64) (uint64, uint64, error) {
	currentBalance, err := getERC20BalanceAtBlock(ctx, client, usdcAddress, wallet, nil)
	if err != nil || currentBalance.Sign() == 0 {
		// Check if there's any MON balance change
		return findFirstMONReceived(ctx, client, wallet, currentBlock)
	}

	lo := uint64(0)
	hi := currentBlock

	// Limit binary search iterations to avoid too many RPC calls
	for i := 0; i < 30 && lo < hi; i++ {
		mid := lo + (hi-lo)/2
		bal, err := getERC20BalanceAtBlock(ctx, client, usdcAddress, wallet, new(big.Int).SetUint64(mid))
		if err != nil {
			lo = mid + 1
			continue
		}
		if bal.Sign() > 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	return lo, currentBlock, nil
}

// findFirstMONReceived binary searches for when MON balance first became > 0
func findFirstMONReceived(ctx context.Context, client *ethclient.Client, wallet common.Address, currentBlock uint64) (uint64, uint64, error) {
	currentBalance, err := client.BalanceAt(ctx, wallet, nil)
	if err != nil || currentBalance.Sign() == 0 {
		return 0, 0, fmt.Errorf("wallet has no activity")
	}

	lo := uint64(0)
	hi := currentBlock

	for i := 0; i < 30 && lo < hi; i++ {
		mid := lo + (hi-lo)/2
		bal, err := client.BalanceAt(ctx, wallet, new(big.Int).SetUint64(mid))
		if err != nil {
			lo = mid + 1
			continue
		}
		if bal.Sign() > 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	return lo, currentBlock, nil
}

func getERC20BalanceAtBlock(ctx context.Context, client *ethclient.Client, token, owner common.Address, block *big.Int) (*big.Int, error) {
	selector, _ := hex.DecodeString("70a08231")
	data := append(selector, common.LeftPadBytes(owner.Bytes(), 32)...)

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &token,
		Data: data,
	}, block)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(result), nil
}

// paginatedFilterLogs with adaptive chunk sizing (starts at 2000, halves on error, min 50)
func paginatedFilterLogs(ctx context.Context, client *ethclient.Client, from, to uint64, addresses []common.Address, topics [][]common.Hash) ([]ethtypes.Log, error) {
	var allLogs []ethtypes.Log
	currentChunk := uint64(2000)

	for start := from; start <= to; {
		end := start + currentChunk - 1
		if end > to {
			end = to
		}

		logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
			Addresses: addresses,
			Topics:    topics,
		})
		if err != nil {
			if currentChunk > 50 {
				currentChunk /= 2
				continue
			}
			start = end + 1
			continue
		}
		allLogs = append(allLogs, logs...)
		start = end + 1
	}

	return allLogs, nil
}

func sumTransfers(logs []ethtypes.Log) float64 {
	total := 0.0
	for _, log := range logs {
		amount := new(big.Int).SetBytes(log.Data)
		total += float64(amount.Int64()) / 1e6
	}
	return total
}

func resolveContract(addr common.Address) string {
	switch addr {
	case diamondAddress:
		return "Diamond"
	case candleRushAddress:
		return "CandleRush"
	case usdcAddress:
		return "USDC"
	}
	if sym, ok := assetSymbols[addr]; ok {
		return sym
	}
	return addr.Hex()[:14] + "..."
}

func resolveAsset(addr common.Address) string {
	if sym, ok := assetSymbols[addr]; ok {
		return sym
	}
	return addr.Hex()[:10] + "..."
}

func formatPnL(pnl float64, green, red func(a ...interface{}) string) string {
	if pnl >= 0 {
		return green(fmt.Sprintf("+%.4f USDC", pnl))
	}
	return red(fmt.Sprintf("%.4f USDC", pnl))
}

func parsePredictAndBetLog(log ethtypes.Log, green, red, cyan func(a ...interface{}) string) {
	if len(log.Topics) < 3 {
		return
	}
	betID := new(big.Int).SetBytes(log.Topics[2].Bytes())
	data := log.Data

	if len(data) < 320 {
		fmt.Printf("    Block %d | Bet #%s | TX %s (short data)\n",
			log.BlockNumber, betID, cyan(log.TxHash.Hex()[:18]+"..."))
		return
	}

	amountRaw := new(big.Int).SetBytes(data[32:64])
	amountFloat := float64(amountRaw.Int64()) / 1e6
	pairAddr := common.BytesToAddress(data[64:96])
	asset := resolveAsset(pairAddr)
	price := new(big.Int).SetBytes(data[160:192]).Uint64()
	isUp := new(big.Int).SetBytes(data[224:256]).Uint64() == 1
	period := uint8(new(big.Int).SetBytes(data[288:320]).Uint64())
	tf := periodTimeframes[period]
	if tf == "" {
		tf = fmt.Sprintf("p%d", period)
	}

	direction := green("UP")
	if !isUp {
		direction = red("DOWN")
	}

	fmt.Printf("    Block %d | Bet #%s | %s %s %s | %.4f USDC | $%.2f | TX %s\n",
		log.BlockNumber, betID, asset, direction, tf,
		amountFloat, float64(price)/1e8, cyan(log.TxHash.Hex()[:18]+"..."))
}

func parseRelativeBetLog(log ethtypes.Log, _, _, cyan func(a ...interface{}) string) {
	if len(log.Topics) < 3 {
		return
	}
	betID := new(big.Int).SetBytes(log.Topics[2].Bytes())
	fmt.Printf("    Block %d | Relative Bet #%s | TX %s\n",
		log.BlockNumber, betID, cyan(log.TxHash.Hex()[:18]+"..."))
}

func getERC20Balance(ctx context.Context, client *ethclient.Client, token, owner common.Address) (*big.Int, error) {
	return getERC20BalanceAtBlock(ctx, client, token, owner, nil)
}
