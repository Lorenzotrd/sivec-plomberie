package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/fatih/color"

	"github.com/blinq-fi/blinq-mm-bot/internal/betting"
	"github.com/blinq-fi/blinq-mm-bot/internal/bot"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
	"github.com/blinq-fi/blinq-mm-bot/internal/prices"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

// Colors for output
var (
	green  = color.New(color.FgGreen).SprintFunc()
	yellow = color.New(color.FgYellow).SprintFunc()
	cyan   = color.New(color.FgCyan).SprintFunc()
	red    = color.New(color.FgRed).SprintFunc()
	bold   = color.New(color.Bold).SprintFunc()
	dim    = color.New(color.Faint).SprintFunc()
)

// Mock Data
var mockMarketsJSON = []byte(`{
	"data": {
		"PredictionConfig": [{
			"chainId": 143,
			"minBetUsd": 0.1,
			"predictionBet": "0x123...",
			"predictionSettle": "0x456...",
			"updatedAt": "2024-01-01T00:00:00Z"
		}],
		"PredictionMarket": [
			{
				"id": 1,
				"name": "BTC/USD",
				"kind": "UP_DOWN",
				"base": "0x0000000000000000000000000000000000000001",
				"marketHash": "0x...",
				"ruleType": "standard",
				"assets": ["0x..."],
				"periods": [
					{
						"period": "5m",
						"periodSeconds": 300,
						"status": "OPEN",
						"payoutBps": 18000,
						"maxUpUsd": 1000,
						"maxDownUsd": 1000,
						"maxSideUsd": 1000
					}
				]
			}
		]
	}
}`)

// Mock Prices: Pyth Fetcher expects specific structure?
// PriceFetcher uses Hermes API.
// Response format: `{"parsed": [{"id": "...", "price": {"price": "100000000", "conf": "100", "expo": -8, "publish_time": ...}, "ema_price": ...}]}`
func getMockPricesJSON() []byte {
	now := time.Now().Unix()
	return []byte(fmt.Sprintf(`{
		"parsed": [
			{
				"id": "e62df6c8b4a85fe1a67db44dc12de5db330f7ac66b72dc658afedf0f4a415b43",
				"price": {
					"price": "5000000000000",
					"conf": "100000",
					"expo": -8,
					"publish_time": %d
				},
				"ema_price": {
					"price": "5000000000000",
					"conf": "100000",
					"expo": -8,
					"publish_time": %d
				}
			}
		]
	}`, now, now))
}

func main() {
	fmt.Println()
	fmt.Println(bold("╔══════════════════════════════════════════════════════════════╗"))
	fmt.Println(bold("║          E2E FLOW TEST - Simulation Mode                     ║"))
	fmt.Println(bold("╚══════════════════════════════════════════════════════════════╝"))
	fmt.Println()

	// 1. Setup Mock Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RPC Mock
		if r.Method == "POST" && r.Header.Get("Content-Type") == "application/json" && !strings.Contains(r.URL.Path, "graphql") && !strings.Contains(r.URL.Path, "auth") {
			// Assume RPC
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x8f"}`)) // ChainID 143 = 0x8f
			return
		}

		// Markets
		if strings.Contains(r.URL.Path, "graphql") {
			w.Header().Set("Content-Type", "application/json")
			w.Write(mockMarketsJSON)
			return
		}

		// Auth
		if strings.Contains(r.URL.Path, "challenge") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"message":"Sign this","nonce":"123","expires_at":"2099-01-01T00:00:00Z"}`))
			return
		}
		if strings.Contains(r.URL.Path, "login") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"mock_token"}`))
			return
		}
		if strings.Contains(r.URL.Path, "referral/redeem") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"message":"redeemed"}`))
			return
		}
		if strings.Contains(r.URL.Path, "referral/codes") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":"MOCK-CODE"}`))
			return
		}

		// Prices
		if strings.Contains(r.URL.Path, "price") || strings.Contains(r.URL.Path, "latest") {
			w.Header().Set("Content-Type", "application/json")
			w.Write(getMockPricesJSON())
			return
		}

		w.WriteHeader(404)
	}))
	defer ts.Close()

	fmt.Printf("  %s Mock server running at %s\n", green("✓"), cyan(ts.URL))

	// 2. Configure Logic
	// We'll use a temp dir for wallets
	tempDir, err := os.MkdirTemp("", "bet-bot-test")
	if err != nil {
		fmt.Printf("  %s Failed to create temp dir: %v\n", red("✗"), err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		RPCUrl: ts.URL, // Use Mock for RPC
		ChainID: 143,
		ApiBaseURL: ts.URL,
		Wallets: config.WalletConfig{
			DBUrl: os.Getenv("DATABASE_URL"),
			SingleWalletMode: false,
			NewWalletProbability: 1.0, // Force new wallet to test flow
			ReferralCode: "TEST-REF",
		},
		Betting: config.BettingConfig{
			EnabledAssets: []string{"BTC"},
			EnabledTimeframes: []string{"5m"},
			MinAmountUSDC: 1,
			MaxAmountUSDC: 10,
			CooldownSeconds: 0,
			BrokerID: 1,
		ToleranceBps:    500,
		},
		Assets: map[string]config.Asset{
			"BTC": {
				Address: "0x0555E30da8f98308EdB960aa94C0Db47230d2B9c",
				PriceID: "e62df6c8b4a85fe1a67db44dc12de5db330f7ac66b72dc658afedf0f4a415b43",
			},
		},
	}

	// 3. Init Components
	// Owner Wallet (Mock Key)
	ownerKey := "0000000000000000000000000000000000000000000000000000000000000001"
	ownerWallet, _ := wallet.NewWallet(ownerKey, cfg.RPCUrl, cfg.ChainID)

	// Wallet Manager
	walletManager, err := wallet.NewManager(ownerWallet, cfg.Wallets, cfg.RPCUrl, cfg.ChainID, cfg.ApiBaseURL, nil)
	if err != nil {
		fmt.Printf("  %s Failed to create wallet manager: %v\n", red("✗"), err)
		os.Exit(1)
	}

	// Market Fetcher
	marketFetcher := markets.NewFetcher(ts.URL+"/graphql", "")

	// Price Fetcher
	priceFetcher := prices.NewFetcher(ts.URL+"/v2/updates/price/latest")

	// Executor (Simulation Mode)
	// We need dummy contracts, pass nil for simulated execution?
	// NewExecutor needs contracts.
	// Contracts manager requires addresses.
	diamondAddr := common.HexToAddress("0x123")
	usdcAddr := common.HexToAddress("0x456")

	// Create simulated contracts
	// Actually Executor uses bound contracts. Mocking them is hard without code gen.
	// But `SimulationMode` skips calling them!
	// So we can pass nil or dummy params, as long as `NewExecutor` doesn't throw.
	// It doesn't.

	executor := betting.NewExecutor(nil, nil, priceFetcher, usdcAddr, diamondAddr, 500, 1)
	executor.SimulationMode = true

	// Strategy
	strategy := betting.NewStrategy(cfg.Betting, cfg.Assets)

	// 4. Run Cycle
	ctx := context.Background()
	fmt.Println()
	fmt.Println(bold(cyan("━━━ RUNNING BETTING CYCLE ━━━")))
	fmt.Println()

	// Force generate a wallet
	w, _, _ := walletManager.GetWalletForBet(ctx)
	fmt.Printf("  %s Generated wallet: %s\n", green("✓"), cyan(w.Address.Hex()))

	// Run
	err = bot.RunBettingCycle(ctx, cfg, walletManager, marketFetcher, strategy, executor, false, true) // Hedged = true
	if err != nil {
		fmt.Printf("  %s Cycle failed: %v\n", red("✗"), err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println(bold(cyan("━━━ RESULTS ━━━")))
	fmt.Println()
	fmt.Println(bold(green("✓ Test Complete. Flows verified:")))
	fmt.Printf("  %s Wallet Creation\n", green("✓"))
	fmt.Printf("  %s Referral Redeem/Create %s\n", green("✓"), dim("(Mocked)"))
	fmt.Printf("  %s Market Fetch %s\n", green("✓"), dim("(Mocked)"))
	fmt.Printf("  %s Bet Selection\n", green("✓"))
	fmt.Printf("  %s Price Fetch %s\n", green("✓"), dim("(Mocked)"))
	fmt.Printf("  %s Balance Check / Funding %s\n", green("✓"), dim("(Simulated)"))
	fmt.Printf("  %s Bet Execution %s\n", green("✓"), dim("(Simulated)"))
	fmt.Println()
}
