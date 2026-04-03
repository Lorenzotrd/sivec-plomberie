package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/fatih/color"
)

// Contract addresses (from config.yaml)
var (
	diamondAddress    = common.HexToAddress("0x928dc8afe312df45576b15b08c086c5427fd8207")
	candleRushAddress = common.HexToAddress("0x8EC30B1b03e6bB529149fF89aA1F9a9BaB2CB26a")
	usdcAddress       = common.HexToAddress("0x754704Bc059F8C67012fEd69BC8A327a5aafb603")

	// Asset address -> symbol mapping
	assetSymbols = map[common.Address]string{
		common.HexToAddress("0x0555E30da8f98308EdB960aa94C0Db47230d2B9c"): "BTC",
		common.HexToAddress("0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242"): "ETH",
		common.HexToAddress("0xea17E5a9efEBf1477dB45082d67010E2245217f1"): "SOL",
	}

	// Period ID -> timeframe
	periodTimeframes = map[uint8]string{
		0: "1m", 1: "5m", 2: "10m", 3: "15m", 4: "30m", 5: "1h",
	}
)

// Event signatures
var (
	predictAndBetPendingTopic  = crypto.Keccak256Hash([]byte("PredictAndBetPending(address,uint256,(address,uint96,address,uint96,address,uint64,uint24,bool,uint128,uint8))"))
	predictRelativePendingTopic = crypto.Keccak256Hash([]byte("PredictRelativePending(address,uint256,(address,uint96,uint96,address,bytes32,uint8,uint8,uint8,uint24,uint128,uint64[]))"))
	transferTopic              = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

const (
	chunkSize       = uint64(100)  // Monad public RPC limit
	lookbackBlocks  = uint64(500000) // ~2-3 days on Monad
	requestTimeout  = 120 * time.Second
)

// paginatedFilterLogs queries eth_getLogs in chunks of chunkSize blocks
func paginatedFilterLogs(ctx context.Context, client *ethclient.Client, from, to uint64, addresses []common.Address, topics [][]common.Hash) ([]ethtypes.Log, error) {
	var allLogs []ethtypes.Log

	for start := from; start <= to; start += chunkSize {
		end := start + chunkSize - 1
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
			// Log warning but continue scanning
			fmt.Printf("    (warn: error at blocks %d-%d: %v)\n", start, end, err)
			continue
		}
		allLogs = append(allLogs, logs...)

		// Progress indicator every 1000 chunks
		scanned := end - from + 1
		total := to - from + 1
		if scanned%(chunkSize*1000) == 0 || end == to {
			pct := float64(scanned) / float64(total) * 100
			fmt.Printf("    Scanned %d/%d blocks (%.0f%%)\r", scanned, total, pct)
		}
	}
	fmt.Println() // newline after progress

	return allLogs, nil
}

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
		fmt.Fprintf(os.Stderr, "Failed to connect to RPC: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	fmt.Printf("\n%s Tracking wallet: %s\n\n", bold("BLINQ BET TRACKER"), cyan(wallet))

	// Get current block
	blockNum, err := client.BlockNumber(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get block number: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Current block: %s\n", cyan(blockNum))

	// Check balances
	fmt.Printf("\n%s\n", bold("=== BALANCES ==="))
	monBalance, err := client.BalanceAt(ctx, walletAddr, nil)
	if err == nil {
		monFloat := new(big.Float).Quo(new(big.Float).SetInt(monBalance), new(big.Float).SetFloat64(1e18))
		fmt.Printf("MON:  %s\n", green(monFloat.Text('f', 4)))
	}

	usdcBalance, err := getERC20Balance(ctx, client, usdcAddress, walletAddr)
	if err == nil {
		usdcFloat := float64(usdcBalance.Int64()) / 1e6
		fmt.Printf("USDC: %s\n", green(fmt.Sprintf("%.2f", usdcFloat)))
	}

	// Determine scan range
	fromBlock := uint64(0)
	if blockNum > lookbackBlocks {
		fromBlock = blockNum - lookbackBlocks
	}

	totalBlocks := blockNum - fromBlock
	totalChunks := totalBlocks / chunkSize
	fmt.Printf("\nScanning %s blocks (%d chunks of %d) from block %d to %d...\n",
		cyan(totalBlocks), totalChunks, chunkSize, fromBlock, blockNum)

	userTopic := common.BytesToHash(walletAddr.Bytes())

	// ===== UP/DOWN BETS =====
	fmt.Printf("\n%s\n", bold("=== UP/DOWN BETS (Diamond PredictAndBetPending) ==="))
	upDownLogs, err := paginatedFilterLogs(ctx, client, fromBlock, blockNum,
		[]common.Address{diamondAddress},
		[][]common.Hash{{predictAndBetPendingTopic}, {userTopic}},
	)
	if err != nil {
		fmt.Printf("  %s Failed: %v\n", red("!"), err)
	} else if len(upDownLogs) == 0 {
		fmt.Printf("  No UP/DOWN bets found\n")
	} else {
		fmt.Printf("  Found %s UP/DOWN bets:\n\n", yellow(len(upDownLogs)))
		for _, log := range upDownLogs {
			parsePredictAndBetLog(log, green, red, cyan)
		}
	}

	// ===== RELATIVE BETS =====
	fmt.Printf("\n%s\n", bold("=== RELATIVE BETS (Diamond PredictRelativePending) ==="))
	relativeLogs, err := paginatedFilterLogs(ctx, client, fromBlock, blockNum,
		[]common.Address{diamondAddress},
		[][]common.Hash{{predictRelativePendingTopic}, {userTopic}},
	)
	if err != nil {
		fmt.Printf("  %s Failed: %v\n", red("!"), err)
	} else if len(relativeLogs) == 0 {
		fmt.Printf("  No RELATIVE bets found\n")
	} else {
		fmt.Printf("  Found %s RELATIVE bets:\n\n", yellow(len(relativeLogs)))
		for _, log := range relativeLogs {
			parseRelativeBetLog(log, green, red, cyan)
		}
	}

	// ===== USDC FLOWS =====
	fromTopic := common.BytesToHash(walletAddr.Bytes())
	toWalletTopic := common.BytesToHash(walletAddr.Bytes())

	// --- Diamond: USDC sent (bets placed) ---
	fmt.Printf("\n%s\n", bold("=== DIAMOND: USDC SENT (bets) ==="))
	diamondToTopic := common.BytesToHash(diamondAddress.Bytes())
	diamondBetLogs, err := paginatedFilterLogs(ctx, client, fromBlock, blockNum,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {fromTopic}, {diamondToTopic}},
	)
	totalDiamondBet := printUSDCTransfers(diamondBetLogs, err, "Diamond bets", yellow, cyan, red)

	// --- Diamond: USDC received (claims/winnings) ---
	fmt.Printf("\n%s\n", bold("=== DIAMOND: USDC RECEIVED (claims) ==="))
	diamondFromTopic := common.BytesToHash(diamondAddress.Bytes())
	diamondClaimLogs, err := paginatedFilterLogs(ctx, client, fromBlock, blockNum,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {diamondFromTopic}, {toWalletTopic}},
	)
	totalDiamondClaim := printUSDCTransfers(diamondClaimLogs, err, "Diamond claims", green, cyan, red)

	// --- CandleRush: USDC sent (bets placed) ---
	fmt.Printf("\n%s\n", bold("=== CANDLE RUSH: USDC SENT (bets) ==="))
	crToTopic := common.BytesToHash(candleRushAddress.Bytes())
	crBetLogs, err := paginatedFilterLogs(ctx, client, fromBlock, blockNum,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {fromTopic}, {crToTopic}},
	)
	totalCRBet := printUSDCTransfers(crBetLogs, err, "CandleRush bets", yellow, cyan, red)

	// --- CandleRush: USDC received (claims/winnings) ---
	fmt.Printf("\n%s\n", bold("=== CANDLE RUSH: USDC RECEIVED (claims) ==="))
	crFromTopic := common.BytesToHash(candleRushAddress.Bytes())
	crClaimLogs, err := paginatedFilterLogs(ctx, client, fromBlock, blockNum,
		[]common.Address{usdcAddress},
		[][]common.Hash{{transferTopic}, {crFromTopic}, {toWalletTopic}},
	)
	totalCRClaim := printUSDCTransfers(crClaimLogs, err, "CandleRush claims", green, cyan, red)

	// ===== SUMMARY =====
	fmt.Printf("\n%s\n", bold("========== SUMMARY =========="))

	totalBet := totalDiamondBet + totalCRBet
	totalClaimed := totalDiamondClaim + totalCRClaim
	pnl := totalClaimed - totalBet

	fmt.Printf("  Diamond   — Bet: %s USDC | Claimed: %s USDC | P&L: %s\n",
		yellow(fmt.Sprintf("%.2f", totalDiamondBet)),
		green(fmt.Sprintf("%.2f", totalDiamondClaim)),
		formatPnL(totalDiamondClaim-totalDiamondBet, green, red),
	)
	fmt.Printf("  CandleRush— Bet: %s USDC | Claimed: %s USDC | P&L: %s\n",
		yellow(fmt.Sprintf("%.2f", totalCRBet)),
		green(fmt.Sprintf("%.2f", totalCRClaim)),
		formatPnL(totalCRClaim-totalCRBet, green, red),
	)
	fmt.Printf("  ─────────────────────────────────────────\n")
	fmt.Printf("  TOTAL     — Bet: %s USDC | Claimed: %s USDC | P&L: %s\n",
		yellow(fmt.Sprintf("%.2f", totalBet)),
		green(fmt.Sprintf("%.2f", totalClaimed)),
		formatPnL(pnl, green, red),
	)

	// Transaction count
	nonce, err := client.NonceAt(ctx, walletAddr, nil)
	if err == nil {
		fmt.Printf("\n  Total transactions: %s\n", cyan(nonce))
	}

	fmt.Println()
}

func printUSDCTransfers(logs []ethtypes.Log, err error, label string, amountColor, cyan func(a ...interface{}) string, red func(a ...interface{}) string) float64 {
	if err != nil {
		fmt.Printf("  %s Failed to fetch %s: %v\n", red("!"), label, err)
		return 0
	}
	if len(logs) == 0 {
		fmt.Printf("  No %s found\n", label)
		return 0
	}

	total := 0.0
	fmt.Printf("  Found %d %s:\n", len(logs), label)
	for _, log := range logs {
		amount := new(big.Int).SetBytes(log.Data)
		amountFloat := float64(amount.Int64()) / 1e6
		total += amountFloat
		fmt.Printf("    Block %d | TX %s | %s USDC\n",
			log.BlockNumber,
			cyan(log.TxHash.Hex()[:18]+"..."),
			amountColor(fmt.Sprintf("%.4f", amountFloat)),
		)
	}
	fmt.Printf("  Total: %s USDC\n", amountColor(fmt.Sprintf("%.2f", total)))
	return total
}

func formatPnL(pnl float64, green, red func(a ...interface{}) string) string {
	if pnl >= 0 {
		return green(fmt.Sprintf("+%.2f USDC", pnl))
	}
	return red(fmt.Sprintf("%.2f USDC", pnl))
}

func parsePredictAndBetLog(log ethtypes.Log, green, red, cyan func(a ...interface{}) string) {
	if len(log.Topics) < 3 {
		return
	}

	betID := new(big.Int).SetBytes(log.Topics[2].Bytes())
	data := log.Data

	if len(data) < 320 {
		fmt.Printf("    Block %d | Bet #%s | TX %s (data too short to decode)\n",
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

	priceFloat := float64(price) / 1e8

	fmt.Printf("    Block %d | Bet #%s | %s %s %s | %.2f USDC | Price $%.2f | TX %s\n",
		log.BlockNumber, betID, asset, direction, tf,
		amountFloat, priceFloat, cyan(log.TxHash.Hex()[:18]+"..."),
	)
}

func parseRelativeBetLog(log ethtypes.Log, _, _, cyan func(a ...interface{}) string) {
	if len(log.Topics) < 3 {
		return
	}

	betID := new(big.Int).SetBytes(log.Topics[2].Bytes())

	fmt.Printf("    Block %d | Relative Bet #%s | TX %s\n",
		log.BlockNumber, betID, cyan(log.TxHash.Hex()[:18]+"..."),
	)
}

func resolveAsset(addr common.Address) string {
	if sym, ok := assetSymbols[addr]; ok {
		return sym
	}
	return addr.Hex()[:10] + "..."
}

func getERC20Balance(ctx context.Context, client *ethclient.Client, token, owner common.Address) (*big.Int, error) {
	selector, _ := hex.DecodeString("70a08231")
	data := append(selector, common.LeftPadBytes(owner.Bytes(), 32)...)

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &token,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	return new(big.Int).SetBytes(result), nil
}
