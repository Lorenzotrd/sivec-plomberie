package wallet

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StoredWallet struct {
	Address          string    `json:"address"`
	PrivateKey       string    `json:"private_key"`
	CreatedAt        time.Time `json:"created_at"`
	LastUsed         time.Time `json:"last_used"`
	BetCount         int       `json:"bet_count"`
	TotalVolumeUSD   float64   `json:"total_volume_usdc"`
	LastBalanceUSD   float64   `json:"last_balance_usd"`
	ReferralRedeemed bool      `json:"referral_redeemed"`
	MyReferralCode   string    `json:"my_referral_code"`
}

type Store struct {
	mu       sync.RWMutex
	pool     *pgxpool.Pool
	keystore *Keystore
}

func NewStore(connStr string, ks *Keystore) (*Store, error) {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &Store{
		pool:     pool,
		keystore: ks,
	}

	if err := s.ensureTable(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to create wallets table: %w", err)
	}

	if err := s.migrateIfNeeded(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("keystore migration failed: %w", err)
	}

	return s, nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) ensureTable(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
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
	return err
}

func (s *Store) migrateIfNeeded() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()

	if s.keystore.Enabled() {
		rows, err := s.pool.Query(ctx, `SELECT address, private_key FROM market_maker_wallets`)
		if err != nil {
			return err
		}
		defer rows.Close()

		type keyPair struct {
			address string
			key     string
		}
		var toMigrate []keyPair

		for rows.Next() {
			var addr, key string
			if err := rows.Scan(&addr, &key); err != nil {
				return err
			}
			if IsPlaintextKey(key) {
				toMigrate = append(toMigrate, keyPair{addr, key})
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, kp := range toMigrate {
			encrypted, err := s.keystore.Encrypt(kp.key)
			if err != nil {
				return fmt.Errorf("failed to encrypt key for %s: %w", kp.address, err)
			}
			_, err = s.pool.Exec(ctx, `UPDATE market_maker_wallets SET private_key = $1 WHERE address = $2`, encrypted, kp.address)
			if err != nil {
				return fmt.Errorf("failed to update encrypted key for %s: %w", kp.address, err)
			}
		}
		if len(toMigrate) > 0 {
			fmt.Printf("  Keystore: Encrypted %d plaintext keys\n", len(toMigrate))
		}
	} else {
		var encAddr string
		err := s.pool.QueryRow(ctx, `SELECT address FROM market_maker_wallets WHERE private_key LIKE 'enc:%' LIMIT 1`).Scan(&encAddr)
		if err == nil {
			return fmt.Errorf("encrypted keys found but no KEYSTORE_PASSPHRASE set")
		}
		if err != pgx.ErrNoRows {
			return err
		}
	}
	return nil
}

func (s *Store) DecryptKey(w *StoredWallet) (string, error) {
	if s.keystore.Enabled() {
		return s.keystore.Decrypt(w.PrivateKey)
	}
	if IsEncrypted(w.PrivateKey) {
		return "", fmt.Errorf("encrypted keys found but no KEYSTORE_PASSPHRASE set")
	}
	return w.PrivateKey, nil
}

func (s *Store) AddWallet(w StoredWallet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.keystore.Enabled() {
		encrypted, err := s.keystore.Encrypt(w.PrivateKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt key: %w", err)
		}
		w.PrivateKey = encrypted
	}

	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO market_maker_wallets (address, private_key, created_at, last_used, bet_count, total_volume_usd, last_balance_usd, referral_redeemed, my_referral_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (address) DO NOTHING
	`, w.Address, w.PrivateKey, w.CreatedAt, w.LastUsed, w.BetCount, w.TotalVolumeUSD, w.LastBalanceUSD, w.ReferralRedeemed, w.MyReferralCode)
	return err
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM market_maker_wallets`).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func (s *Store) GetRandomWallet() (*StoredWallet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()

	count := 0
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM market_maker_wallets`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("no wallets available")
	}

	offset := rand.Intn(count)
	var w StoredWallet
	err := s.pool.QueryRow(ctx, `
		SELECT address, private_key, created_at, last_used, bet_count, total_volume_usd, last_balance_usd, referral_redeemed, my_referral_code
		FROM market_maker_wallets
		ORDER BY address
		OFFSET $1 LIMIT 1
	`, offset).Scan(&w.Address, &w.PrivateKey, &w.CreatedAt, &w.LastUsed, &w.BetCount, &w.TotalVolumeUSD, &w.LastBalanceUSD, &w.ReferralRedeemed, &w.MyReferralCode)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Store) GetWalletByAddress(address string) (*StoredWallet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	var w StoredWallet
	err := s.pool.QueryRow(ctx, `
		SELECT address, private_key, created_at, last_used, bet_count, total_volume_usd, last_balance_usd, referral_redeemed, my_referral_code
		FROM market_maker_wallets WHERE address = $1
	`, address).Scan(&w.Address, &w.PrivateKey, &w.CreatedAt, &w.LastUsed, &w.BetCount, &w.TotalVolumeUSD, &w.LastBalanceUSD, &w.ReferralRedeemed, &w.MyReferralCode)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("wallet not found: %s", address)
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Store) UpdateWallet(address string, betAmount float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tag, err := s.pool.Exec(ctx, `
		UPDATE market_maker_wallets SET last_used = $1, bet_count = bet_count + 1, total_volume_usd = total_volume_usd + $2
		WHERE address = $3
	`, time.Now(), betAmount, address)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", address)
	}
	return nil
}

func (s *Store) UpdateBalance(address string, balanceUSD float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tag, err := s.pool.Exec(ctx, `UPDATE market_maker_wallets SET last_balance_usd = $1 WHERE address = $2`, balanceUSD, address)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", address)
	}
	return nil
}

func (s *Store) MarkReferralRedeemed(address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tag, err := s.pool.Exec(ctx, `UPDATE market_maker_wallets SET referral_redeemed = TRUE WHERE address = $1`, address)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", address)
	}
	return nil
}

func (s *Store) UpdateMyReferralCode(address, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tag, err := s.pool.Exec(ctx, `UPDATE market_maker_wallets SET my_referral_code = $1 WHERE address = $2`, code, address)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", address)
	}
	return nil
}

func (s *Store) GetAllWallets() []StoredWallet {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `
		SELECT address, private_key, created_at, last_used, bet_count, total_volume_usd, last_balance_usd, referral_redeemed, my_referral_code
		FROM market_maker_wallets ORDER BY created_at
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var wallets []StoredWallet
	for rows.Next() {
		var w StoredWallet
		if err := rows.Scan(&w.Address, &w.PrivateKey, &w.CreatedAt, &w.LastUsed, &w.BetCount, &w.TotalVolumeUSD, &w.LastBalanceUSD, &w.ReferralRedeemed, &w.MyReferralCode); err != nil {
			return nil
		}
		wallets = append(wallets, w)
	}
	return wallets
}
