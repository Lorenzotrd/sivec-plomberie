package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"

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

	// CandleRush interval -> timeframe
	intervalTimeframes = map[uint32]string{
		300: "5m", 900: "15m", 1800: "30m",
	}
)

// Event signatures
var (
	// PredictAndBetPending(address indexed user, uint256 indexed id, tuple prediction)
	predictAndBetPendingTopic = crypto.Keccak256Hash([]byte("PredictAndBetPending(address,uint256,(address,uint96,address,uint96,address,uint64,uint24,bool,uint128,uint8))"))

	// PredictRelativePending(address indexed user, uint256 indexed id, tuple prediction)
	predictRelativePendingTopic = crypto.Keccak256Hash([]byte("PredictRelativePending(address,uint256,(address,uint96,uint96,address,bytes32,uint8,uint8,uint8,uint24,uint128,uint64[]))"))
)

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	fmt.Printf("\n%s\n", bold("--- BALANCES ---"))
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

	// Query PredictAndBetPending events for this wallet
	// Search last ~200k blocks (~1 day on Monad)
	lookback := uint64(200000)
	fromBlock := uint64(0)
	if blockNum > lookback {
		fromBlock = blockNum - lookback
	}

	fmt.Printf("\n%s (blocks %d - %d)\n", bold("--- UP/DOWN BETS ---"), fromBlock, blockNum)

	// Topic[0] = event signature, Topic[1] = indexed user address
	userTopic := common.BytesToHash(walletAddr.Bytes())

	upDownLogs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(blockNum),
		Addresses: []common.Address{diamondAddress},
		Topics:    [][]common.Hash{{predictAndBetPendingTopic}, {userTopic}},
	})
	if err != nil {
		fmt.Printf("  %s Failed to fetch UP/DOWN logs: %v\n", red("!"), err)
	} else if len(upDownLogs) == 0 {
		fmt.Printf("  No UP/DOWN bets found in this range\n")
	} else {
		fmt.Printf("  Found %s UP/DOWN bets\n\n", yellow(len(upDownLogs)))
		for _, log := range upDownLogs {
			parsePredictAndBetLog(log, green, red, cyan)
		}
	}

	// Query PredictRelativePending events
	fmt.Printf("\n%s (blocks %d - %d)\n", bold("--- RELATIVE BETS ---"), fromBlock, blockNum)
	relativeLogs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(blockNum),
		Addresses: []common.Address{diamondAddress},
		Topics:    [][]common.Hash{{predictRelativePendingTopic}, {userTopic}},
	})
	if err != nil {
		fmt.Printf("  %s Failed to fetch RELATIVE logs: %v\n", red("!"), err)
	} else if len(relativeLogs) == 0 {
		fmt.Printf("  No RELATIVE bets found in this range\n")
	} else {
		fmt.Printf("  Found %s RELATIVE bets\n\n", yellow(len(relativeLogs)))
		for _, log := range relativeLogs {
			parseRelativeBetLog(log, green, red, cyan)
		}
	}

	// Query CandleRush bets via transaction traces
	// CandleRush doesn't emit indexed user events in the ABI we have,
	// so we check USDC Transfer events TO the CandleRush contract FROM our wallet
	fmt.Printf("\n%s (blocks %d - %d)\n", bold("--- CANDLE RUSH ACTIVITY (USDC transfers) ---"), fromBlock, blockNum)

	transferTopic := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	fromTopic := common.BytesToHash(walletAddr.Bytes())
	toTopic := common.BytesToHash(candleRushAddress.Bytes())

	crLogs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(blockNum),
		Addresses: []common.Address{usdcAddress},
		Topics:    [][]common.Hash{{transferTopic}, {fromTopic}, {toTopic}},
	})
	if err != nil {
		fmt.Printf("  %s Failed to fetch CandleRush logs: %v\n", red("!"), err)
	} else if len(crLogs) == 0 {
		fmt.Printf("  No CandleRush USDC transfers found\n")
	} else {
		fmt.Printf("  Found %s CandleRush USDC transfers\n\n", yellow(len(crLogs)))
		totalSpent := 0.0
		for _, log := range crLogs {
			amount := new(big.Int).SetBytes(log.Data)
			amountFloat := float64(amount.Int64()) / 1e6
			totalSpent += amountFloat
			fmt.Printf("  Block %d | TX %s | %s USDC\n",
				log.BlockNumber,
				cyan(log.TxHash.Hex()[:18]+"..."),
				yellow(fmt.Sprintf("%.2f", amountFloat)),
			)
		}
		fmt.Printf("\n  Total USDC to CandleRush: %s\n", red(fmt.Sprintf("%.2f", totalSpent)))
	}

	// Also check USDC transfers FROM CandleRush TO wallet (winnings/claims)
	fmt.Printf("\n%s\n", bold("--- CANDLE RUSH CLAIMS (received) ---"))
	claimFromTopic := common.BytesToHash(candleRushAddress.Bytes())
	claimToTopic := common.BytesToHash(walletAddr.Bytes())

	claimLogs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(blockNum),
		Addresses: []common.Address{usdcAddress},
		Topics:    [][]common.Hash{{transferTopic}, {claimFromTopic}, {claimToTopic}},
	})
	if err != nil {
		fmt.Printf("  %s Failed to fetch claim logs: %v\n", red("!"), err)
	} else if len(claimLogs) == 0 {
		fmt.Printf("  No CandleRush claims found\n")
	} else {
		totalWon := 0.0
		for _, log := range claimLogs {
			amount := new(big.Int).SetBytes(log.Data)
			amountFloat := float64(amount.Int64()) / 1e6
			totalWon += amountFloat
			fmt.Printf("  Block %d | TX %s | +%s USDC\n",
				log.BlockNumber,
				cyan(log.TxHash.Hex()[:18]+"..."),
				green(fmt.Sprintf("%.2f", amountFloat)),
			)
		}
		fmt.Printf("\n  Total claimed from CandleRush: %s\n", green(fmt.Sprintf("%.2f", totalWon)))
	}

	// Also check Diamond contract USDC flows
	fmt.Printf("\n%s\n", bold("--- DIAMOND BETS (USDC sent) ---"))
	diamondToTopic := common.BytesToHash(diamondAddress.Bytes())

	diamondBetLogs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(blockNum),
		Addresses: []common.Address{usdcAddress},
		Topics:    [][]common.Hash{{transferTopic}, {fromTopic}, {diamondToTopic}},
	})
	if err != nil {
		fmt.Printf("  %s Failed to fetch Diamond bet logs: %v\n", red("!"), err)
	} else if len(diamondBetLogs) == 0 {
		fmt.Printf("  No Diamond USDC transfers found\n")
	} else {
		totalBet := 0.0
		for _, log := range diamondBetLogs {
			amount := new(big.Int).SetBytes(log.Data)
			amountFloat := float64(amount.Int64()) / 1e6
			totalBet += amountFloat
			fmt.Printf("  Block %d | TX %s | %s USDC\n",
				log.BlockNumber,
				cyan(log.TxHash.Hex()[:18]+"..."),
				yellow(fmt.Sprintf("%.2f", amountFloat)),
			)
		}
		fmt.Printf("\n  Total USDC to Diamond: %s\n", red(fmt.Sprintf("%.2f", totalBet)))
	}

	fmt.Printf("\n%s\n", bold("--- DIAMOND CLAIMS (received) ---"))
	diamondFromTopic := common.BytesToHash(diamondAddress.Bytes())

	diamondClaimLogs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(blockNum),
		Addresses: []common.Address{usdcAddress},
		Topics:    [][]common.Hash{{transferTopic}, {diamondFromTopic}, {claimToTopic}},
	})
	if err != nil {
		fmt.Printf("  %s Failed to fetch Diamond claim logs: %v\n", red("!"), err)
	} else if len(diamondClaimLogs) == 0 {
		fmt.Printf("  No Diamond claims found\n")
	} else {
		totalClaimed := 0.0
		for _, log := range diamondClaimLogs {
			amount := new(big.Int).SetBytes(log.Data)
			amountFloat := float64(amount.Int64()) / 1e6
			totalClaimed += amountFloat
			fmt.Printf("  Block %d | TX %s | +%s USDC\n",
				log.BlockNumber,
				cyan(log.TxHash.Hex()[:18]+"..."),
				green(fmt.Sprintf("%.2f", amountFloat)),
			)
		}
		fmt.Printf("\n  Total claimed from Diamond: %s\n", green(fmt.Sprintf("%.2f", totalClaimed)))
	}

	// Transaction count
	nonce, err := client.NonceAt(ctx, walletAddr, nil)
	if err == nil {
		fmt.Printf("\n%s Total transactions sent: %s\n", bold("---"), cyan(nonce))
	}

	fmt.Println()
}

func parsePredictAndBetLog(log ethtypes.Log, green, red, cyan func(a ...interface{}) string) {
	if len(log.Topics) < 3 {
		return
	}

	betID := new(big.Int).SetBytes(log.Topics[2].Bytes())
	data := log.Data

	if len(data) < 320 {
		fmt.Printf("  Block %d | Bet #%s | TX %s (data too short to decode)\n",
			log.BlockNumber, betID, cyan(log.TxHash.Hex()[:18]+"..."))
		return
	}

	// Decode the prediction tuple from non-indexed data
	// Layout: tokenIn(address), amountIn(uint96), predictionPairBase(address), openFee(uint96),
	//         user(address), price(uint64), broker(uint24), isUp(bool), blockNumber(uint128), period(uint8)
	// Each field is 32-byte padded in ABI encoding

	// tokenIn at offset 0
	// amountIn at offset 32
	amountRaw := new(big.Int).SetBytes(data[32:64])
	amountFloat := float64(amountRaw.Int64()) / 1e6

	// predictionPairBase at offset 64
	pairAddr := common.BytesToAddress(data[64:96])
	asset := resolveAsset(pairAddr)

	// price at offset 160
	price := new(big.Int).SetBytes(data[160:192]).Uint64()

	// isUp at offset 224
	isUp := new(big.Int).SetBytes(data[224:256]).Uint64() == 1

	// period at offset 288
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

	fmt.Printf("  Block %d | Bet #%s | %s %s %s | %.2f USDC | Price $%.2f | TX %s\n",
		log.BlockNumber,
		betID,
		asset,
		direction,
		tf,
		amountFloat,
		priceFloat,
		cyan(log.TxHash.Hex()[:18]+"..."),
	)
}

func parseRelativeBetLog(log ethtypes.Log, green, red, cyan func(a ...interface{}) string) {
	if len(log.Topics) < 3 {
		return
	}

	betID := new(big.Int).SetBytes(log.Topics[2].Bytes())

	fmt.Printf("  Block %d | Relative Bet #%s | TX %s\n",
		log.BlockNumber,
		betID,
		cyan(log.TxHash.Hex()[:18]+"..."),
	)
}

func resolveAsset(addr common.Address) string {
	if sym, ok := assetSymbols[addr]; ok {
		return sym
	}
	return addr.Hex()[:10] + "..."
}

func getERC20Balance(ctx context.Context, client *ethclient.Client, token, owner common.Address) (*big.Int, error) {
	// balanceOf(address) selector = 0x70a08231
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

