package wallet

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/fatih/color"

	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/referral"
	"github.com/blinq-fi/blinq-mm-bot/internal/utils"
)

var (
	mgrGreen  = color.New(color.FgGreen).SprintFunc()
	mgrYellow = color.New(color.FgYellow).SprintFunc()
	mgrCyan   = color.New(color.FgCyan).SprintFunc()
)

type Manager struct {
	store          *Store
	ownerWallet    *Wallet
	config         config.WalletConfig
	rpcURL         string
	chainID        int64
	referralClient *referral.Client
}

func NewManager(ownerWallet *Wallet, cfg config.WalletConfig, rpcURL string, chainID int64, apiBaseURL string, ks *Keystore) (*Manager, error) {
	store, err := NewStore(cfg.DBUrl, ks)
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet store: %w", err)
	}

	// Only create referral client if API base URL is provided
	var refClient *referral.Client
	if apiBaseURL != "" {
		refClient = referral.NewClient(apiBaseURL)
	}

	return &Manager{
		store:          store,
		ownerWallet:    ownerWallet,
		config:         cfg,
		rpcURL:         rpcURL,
		chainID:        chainID,
		referralClient: refClient,
	}, nil
}

func (m *Manager) GetWalletForBet(ctx context.Context) (*Wallet, bool, error) {
	// Single wallet mode: always use owner wallet directly
	if m.config.SingleWalletMode {
		return m.ownerWallet, false, nil
	}

	// If generation is disabled and no wallets exist, return error
	if !m.config.ShouldGenerateNewWallets() && m.store.Count() == 0 {
		return nil, false, fmt.Errorf("no wallets in store and wallet generation is disabled (generate_new_wallets=false). Use --generate to create wallets first")
	}

	shouldCreateNew := m.shouldCreateNewWallet()

	if shouldCreateNew {
		// Generate new wallet
		privateKey, address, err := GenerateWallet()
		if err != nil {
			return nil, false, fmt.Errorf("failed to generate wallet: %w", err)
		}

		// Create stored wallet record
		storedWallet := StoredWallet{
			Address:        address.Hex(),
			PrivateKey:     PrivateKeyToHex(privateKey),
			CreatedAt:      time.Now(),
			LastUsed:       time.Now(),
			BetCount:       0,
			TotalVolumeUSD: 0,
		}

		// Save to store
		if err := m.store.AddWallet(storedWallet); err != nil {
			return nil, false, fmt.Errorf("failed to save wallet: %w", err)
		}

		// Create wallet instance
		wallet, err := NewWallet(storedWallet.PrivateKey, m.rpcURL, m.chainID)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create wallet instance: %w", err)
		}

		// Ensure referral is redeemed for new wallet
		if err := m.ensureReferral(wallet); err != nil {
			fmt.Printf("  %s Failed to redeem referral: %v\n", mgrYellow("!"), err)
		}

		return wallet, true, nil
	}

	// Pick random existing wallet
	storedWallet, err := m.store.GetRandomWallet()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get random wallet: %w", err)
	}

	decryptedKey, err := m.store.DecryptKey(storedWallet)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decrypt wallet key: %w", err)
	}

	wallet, err := NewWallet(decryptedKey, m.rpcURL, m.chainID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create wallet instance: %w", err)
	}

	// Ensure referral is redeemed
	if err := m.ensureReferral(wallet); err != nil {
		fmt.Printf("  %s Failed to redeem referral: %v\n", mgrYellow("!"), err)
	}

	return wallet, false, nil
}

func (m *Manager) ensureReferral(w *Wallet) error {
	// Skip referral if no API client configured (API_BASE_URL not set)
	if m.referralClient == nil {
		return nil
	}

	stored, err := m.store.GetWalletByAddress(w.Address.Hex())
	if err != nil {
		return err
	}

	// Determine what needs to be done
	needsCreate := stored.MyReferralCode == ""
	needsRedeem := m.config.ReferralCode != "" && !stored.ReferralRedeemed

	if !needsCreate && !needsRedeem {
		return nil // Nothing to do
	}

	// Prepare codes
	var createCode string
	if needsCreate {
		createCode = utils.GenerateUniqueReferralCode()
	}
	var redeemCode string
	if needsRedeem {
		redeemCode = m.getRandomReferralCode()
	}

	// Use single auth session for both operations (matching betme-interface flow)
	codeCreated, codeRedeemed, err := m.referralClient.EnsureReferral(w, w.Address.Hex(), createCode, redeemCode)

	if codeCreated {
		m.store.UpdateMyReferralCode(w.Address.Hex(), createCode)
		fmt.Printf("  %s Created referral code: %s\n", mgrGreen("✓"), mgrCyan(createCode))
	}

	if codeRedeemed {
		m.store.MarkReferralRedeemed(w.Address.Hex())
		fmt.Printf("  %s Redeemed referral code: %s\n", mgrGreen("✓"), mgrCyan(redeemCode))
	}

	if err != nil {
		fmt.Printf("  %s Referral error: %v\n", mgrYellow("!"), err)
	}

	return nil
}

// getRandomReferralCode returns a random code from comma-separated list
// e.g., "ABC,DEF,GHI" -> randomly returns "ABC", "DEF", or "GHI"
func (m *Manager) getRandomReferralCode() string {
	if m.config.ReferralCode == "" {
		return ""
	}

	// Split by comma and trim whitespace
	codes := strings.Split(m.config.ReferralCode, ",")
	var validCodes []string
	for _, code := range codes {
		trimmed := strings.TrimSpace(code)
		if trimmed != "" {
			validCodes = append(validCodes, trimmed)
		}
	}

	if len(validCodes) == 0 {
		return ""
	}

	// Randomly select one
	return validCodes[rand.Intn(len(validCodes))]
}

// GetTwoDistinctWallets returns two different random wallets from the store
// Returns wallet1, wallet2, error
func (m *Manager) GetTwoDistinctWallets(ctx context.Context) (*Wallet, *Wallet, error) {
	if m.config.SingleWalletMode {
		return nil, nil, fmt.Errorf("single wallet mode cannot support dual wallet operations")
	}

	w1, _, err := m.GetWalletForBet(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Try to find a second different wallet
	for i := 0; i < 10; i++ { // Retry limit
		w2, _, err := m.GetWalletForBet(ctx)
		if err != nil {
			return nil, nil, err
		}
		if w1.Address != w2.Address {
			return w1, w2, nil
		}
	}

	// If store has only 1 wallet, we can't get two distinct
	if m.store.Count() < 2 {
		return nil, nil, fmt.Errorf("not enough wallets in store to pick 2 distinct (have %d)", m.store.Count())
	}

	// Fallback to explicit differing pick if random failed repeatedly (rare if count > 1)
	wallets := m.store.GetAllWallets()
	// Find one that isn't w1
	for _, w := range wallets {
		if w.Address != w1.Address.Hex() {
			decKey, err := m.store.DecryptKey(&w)
			if err != nil {
				continue
			}
			w2, err := NewWallet(decKey, m.rpcURL, m.chainID)
			if err != nil {
				continue
			}
			// Ensure referral is redeemed for fallback wallet
			if err := m.ensureReferral(w2); err != nil {
				fmt.Printf("  %s Failed to redeem referral: %v\n", mgrYellow("!"), err)
			}
			return w1, w2, nil
		}
	}

	return nil, nil, fmt.Errorf("failed to find two distinct wallets")
}

func (m *Manager) shouldCreateNewWallet() bool {
	// If on-the-fly generation is disabled, never create new wallets
	if !m.config.ShouldGenerateNewWallets() {
		return false
	}

	existingCount := m.store.Count()

	// Always create new if below minimum
	if existingCount < m.config.MinWalletsBeforeReuse {
		return true
	}

	// Random chance based on probability
	return rand.Float64() < m.config.NewWalletProbability
}

func (m *Manager) GetRandomFundAmount() float64 {
	diff := m.config.MaxFundAmountUSDC - m.config.MinFundAmountUSDC
	return m.config.MinFundAmountUSDC + rand.Float64()*diff
}

func (m *Manager) GetRandomGasAmount() float64 {
	diff := m.config.MaxGasFundMON - m.config.MinGasFundMON
	return m.config.MinGasFundMON + rand.Float64()*diff
}

func (m *Manager) GetMinGasBalance() float64 {
	return m.config.MinGasBalanceMON
}

func (m *Manager) RecordBet(address string, betAmount float64) error {
	return m.store.UpdateWallet(address, betAmount)
}

func (m *Manager) GetOwnerWallet() *Wallet {
	return m.ownerWallet
}

func (m *Manager) WalletCount() int {
	return m.store.Count()
}

func (m *Manager) GenerateFromMnemonic(mnemonic string, count int) (int, error) {
	added := 0
	for i := 0; i < count; i++ {
		// We use existing count + i as index to continue the sequence if we already generated some
		// However, for strict determinism from 0, we might want to start from 0 and check all.
		// But if we want unique new ones, we should use an offset.
		// "Trace back" implies user knows the index.
		// Let's assume we want to generate indices 0 to count-1.
		// If the user wants MORE, they might need to specify start index?
		// For simplicity, let's generate 0 to count-1. If they exist, they are skipped.

		_, privateKeyHex, err := DerivePrivateKey(mnemonic, i)
		if err != nil {
			return added, fmt.Errorf("failed to derive key at index %d: %w", i, err)
		}

		wallet, err := NewWallet(privateKeyHex, m.rpcURL, m.chainID)
		if err != nil {
			return added, fmt.Errorf("failed to create wallet instance: %w", err)
		}

		storedWallet := StoredWallet{
			Address:        wallet.Address.Hex(),
			PrivateKey:     privateKeyHex,
			CreatedAt:      time.Now(),
			LastUsed:       time.Now(),
			BetCount:       0,
			TotalVolumeUSD: 0,
		}

		countBefore := m.store.Count()
		if err := m.store.AddWallet(storedWallet); err != nil {
			return added, err
		}
		if m.store.Count() > countBefore {
			added++
		}
	}
	return added, nil
}

func (m *Manager) GetAllWallets() []StoredWallet {
	return m.store.GetAllWallets()
}

// Close closes the underlying wallet store's database connection pool.
func (m *Manager) Close() {
	m.store.Close()
}

// GetStore returns the underlying wallet store for shared access (e.g., sweep).
// Callers must use the Store's exported methods which are mutex-protected.
func (m *Manager) GetStore() *Store {
	return m.store
}

// ToBaseUnits converts a decimal USDC amount to base units (6 decimals)
func ToBaseUnits(amount float64, decimals int) *big.Int {
	multiplier := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	amountFloat := new(big.Float).SetFloat64(amount)
	result := new(big.Float).Mul(amountFloat, multiplier)

	resultInt, _ := result.Int(nil)
	return resultInt
}

// FromBaseUnits converts base units to decimal amount
func FromBaseUnits(amount *big.Int, decimals int) float64 {
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	amountFloat := new(big.Float).SetInt(amount)
	result := new(big.Float).Quo(amountFloat, divisor)

	f, _ := result.Float64()
	return f
}

// GetAddressFromHex converts a hex string to common.Address
func GetAddressFromHex(hex string) common.Address {
	return common.HexToAddress(hex)
}

// ToWei converts a decimal amount to wei (18 decimals for native token)
func ToWei(amount float64) *big.Int {
	return ToBaseUnits(amount, 18)
}

// FromWei converts wei to decimal amount
func FromWei(amount *big.Int) float64 {
	return FromBaseUnits(amount, 18)
}
