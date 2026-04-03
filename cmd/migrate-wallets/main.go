package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

type storedWallet struct {
	Address          string    `json:"address"`
	PrivateKey       string    `json:"private_key"`
	CreatedAt        time.Time `json:"created_at"`
	LastUsed         time.Time `json:"last_used"`
	BetCount         int       `json:"bet_count"`
	TotalVolumeUSD   float64   `json:"total_volume_usdc"`
	ReferralRedeemed bool      `json:"referral_redeemed"`
	MyReferralCode   string    `json:"my_referral_code"`
}

type walletStore struct {
	Wallets []storedWallet `json:"wallets"`
}

func main() {
	jsonPath := flag.String("json-path", "./data/wallets.json", "Path to existing wallets.json file")
	flag.Parse()

	// Load .env file (same as other commands)
	if err := godotenv.Load(); err != nil {
		fmt.Println("Note: no .env file found, using environment variables")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("Error: DATABASE_URL is required (set in .env or environment)")
		os.Exit(1)
	}

	// Read JSON file
	data, err := os.ReadFile(*jsonPath)
	if err != nil {
		fmt.Printf("Error reading %s: %v\n", *jsonPath, err)
		os.Exit(1)
	}

	var store walletStore
	if err := json.Unmarshal(data, &store); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Found %d wallets in %s\n", len(store.Wallets), *jsonPath)

	// Connect to PostgreSQL
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("Error connecting to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Create table if not exists
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS market_maker_wallets (
			address           TEXT PRIMARY KEY,
			private_key       TEXT NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_used         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			bet_count         INTEGER NOT NULL DEFAULT 0,
			total_volume_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
			last_balance_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
			referral_redeemed BOOLEAN NOT NULL DEFAULT FALSE,
			my_referral_code  TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		fmt.Printf("Error creating table: %v\n", err)
		os.Exit(1)
	}

	// Optionally encrypt plaintext keys
	passphrase := wallet.LoadPassphrase()
	var ks *wallet.Keystore
	if passphrase != "" {
		ks = wallet.NewKeystore(passphrase)
		fmt.Println("Keystore enabled: will encrypt plaintext keys during migration")
	}

	inserted := 0
	skipped := 0

	for _, w := range store.Wallets {
		key := w.PrivateKey
		if ks != nil && wallet.IsPlaintextKey(key) {
			encrypted, err := ks.Encrypt(key)
			if err != nil {
				fmt.Printf("  Error encrypting key for %s: %v\n", w.Address, err)
				continue
			}
			key = encrypted
		}

		tag, err := pool.Exec(ctx, `
			INSERT INTO market_maker_wallets (address, private_key, created_at, last_used, bet_count, total_volume_usd, last_balance_usd, referral_redeemed, my_referral_code)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (address) DO NOTHING
		`, w.Address, key, w.CreatedAt, w.LastUsed, w.BetCount, w.TotalVolumeUSD, 0.0, w.ReferralRedeemed, w.MyReferralCode)
		if err != nil {
			fmt.Printf("  Error inserting %s: %v\n", w.Address, err)
			continue
		}
		if tag.RowsAffected() > 0 {
			inserted++
		} else {
			skipped++
		}
	}

	fmt.Printf("\nMigration complete: %d inserted, %d skipped (already exist)\n", inserted, skipped)
}
