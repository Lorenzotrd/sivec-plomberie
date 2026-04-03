package wallet

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// maxGasPriceGwei caps the gas price to prevent draining wallets via a
// malicious or misbehaving RPC endpoint. 10,000 Gwei is generous for any chain.
const maxGasPriceGwei = 10_000

var maxGasPrice = new(big.Int).Mul(big.NewInt(maxGasPriceGwei), big.NewInt(1e9))

type Wallet struct {
	Address    common.Address
	privateKey *ecdsa.PrivateKey
	client     *ethclient.Client
	chainID    *big.Int

	// nonceMu serializes all nonce-acquiring operations so concurrent
	// goroutines sharing the same wallet never get duplicate nonces.
	nonceMu    sync.Mutex
	localNonce uint64
	nonceReady bool
}

func NewWallet(privateKeyHex string, rpcURL string, chainID int64) (*Wallet, error) {
	// Remove 0x prefix if present
	if len(privateKeyHex) > 2 && privateKeyHex[:2] == "0x" {
		privateKeyHex = privateKeyHex[2:]
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("failed to cast public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	return &Wallet{
		Address:    address,
		privateKey: privateKey,
		client:     client,
		chainID:    big.NewInt(chainID),
	}, nil
}

func GenerateWallet() (*ecdsa.PrivateKey, common.Address, error) {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to generate key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, common.Address{}, fmt.Errorf("failed to cast public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)
	return privateKey, address, nil
}

func PrivateKeyToHex(key *ecdsa.PrivateKey) string {
	return fmt.Sprintf("0x%064x", crypto.FromECDSA(key))
}

// nextNonce returns the next nonce for this wallet, using a local counter
// initialised lazily from the chain. Concurrent callers are serialised.
func (w *Wallet) nextNonce(ctx context.Context) (uint64, error) {
	if w.client == nil {
		return 0, fmt.Errorf("wallet has no RPC client (created via NewWalletFromKey)")
	}
	w.nonceMu.Lock()
	defer w.nonceMu.Unlock()

	if !w.nonceReady {
		pending, err := w.client.PendingNonceAt(ctx, w.Address)
		if err != nil {
			return 0, fmt.Errorf("failed to get nonce: %w", err)
		}
		w.localNonce = pending
		w.nonceReady = true
	}

	nonce := w.localNonce
	w.localNonce++
	return nonce, nil
}

// ResetNonce forces the next call to nextNonce to re-fetch from the chain.
// Call this after a broadcast failure to re-sync.
func (w *Wallet) ResetNonce() {
	w.nonceMu.Lock()
	defer w.nonceMu.Unlock()
	w.nonceReady = false
}

// cappedGasPrice returns the suggested gas price, capped at maxGasPrice.
func (w *Wallet) cappedGasPrice(ctx context.Context) (*big.Int, error) {
	if w.client == nil {
		return nil, fmt.Errorf("wallet has no RPC client (created via NewWalletFromKey)")
	}
	gasPrice, err := w.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}
	if gasPrice.Cmp(maxGasPrice) > 0 {
		return new(big.Int).Set(maxGasPrice), nil
	}
	return gasPrice, nil
}

func (w *Wallet) GetTransactOpts(ctx context.Context) (*bind.TransactOpts, error) {
	if w.client == nil {
		return nil, fmt.Errorf("wallet has no RPC client (created via NewWalletFromKey)")
	}
	nonce, err := w.nextNonce(ctx)
	if err != nil {
		return nil, err
	}

	gasPrice, err := w.cappedGasPrice(ctx)
	if err != nil {
		return nil, err
	}

	auth, err := bind.NewKeyedTransactorWithChainID(w.privateKey, w.chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.GasPrice = gasPrice
	auth.Context = ctx

	return auth, nil
}

func (w *Wallet) GetBalance(ctx context.Context, address common.Address) (*big.Int, error) {
	if w.client == nil {
		return nil, fmt.Errorf("wallet has no RPC client (created via NewWalletFromKey)")
	}
	return w.client.BalanceAt(ctx, address, nil)
}

func (w *Wallet) WaitForTx(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if w.client == nil {
		return nil, fmt.Errorf("wallet has no RPC client (created via NewWalletFromKey)")
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			receipt, err := w.client.TransactionReceipt(ctx, txHash)
			if err != nil {
				continue
			}
			return receipt, nil
		}
	}
}

func (w *Wallet) Client() *ethclient.Client {
	return w.client
}

func (w *Wallet) ChainID() *big.Int {
	return w.chainID
}

// SendNative sends native tokens (MON) to an address
func (w *Wallet) SendNative(ctx context.Context, to common.Address, amount *big.Int) (common.Hash, error) {
	if w.client == nil {
		return common.Hash{}, fmt.Errorf("wallet has no RPC client (created via NewWalletFromKey)")
	}
	nonce, err := w.nextNonce(ctx)
	if err != nil {
		return common.Hash{}, err
	}

	gasPrice, err := w.cappedGasPrice(ctx)
	if err != nil {
		return common.Hash{}, err
	}

	gasLimit := uint64(21000) // Standard transfer gas limit

	tx := types.NewTransaction(nonce, to, amount, gasLimit, gasPrice, nil)

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(w.chainID), w.privateKey)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to sign tx: %w", err)
	}

	if err := w.client.SendTransaction(ctx, signedTx); err != nil {
		// Reset nonce on broadcast failure so we re-sync from chain next time
		w.ResetNonce()
		return common.Hash{}, fmt.Errorf("failed to send tx: %w", err)
	}

	return signedTx.Hash(), nil
}

// SignMessage signs a message using EIP-191 standard
func (w *Wallet) SignMessage(message string) (string, error) {
	data := []byte(message)
	hash := crypto.Keccak256Hash(
		[]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(data), data)),
	)

	signature, err := crypto.Sign(hash.Bytes(), w.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	// Adjust V value (Go-ethereum crypto.Sign returns 0/1, we need 27/28 for standard RPC)
	if len(signature) == 65 {
		signature[64] += 27
	}

	return fmt.Sprintf("0x%x", signature), nil
}

// NewWalletFromKey creates a wallet from an ECDSA private key without RPC connection
// Useful for address generation without network calls
func NewWalletFromKey(privateKey *ecdsa.PrivateKey) (*Wallet, error) {
	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("failed to cast public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return &Wallet{
		Address:    address,
		privateKey: privateKey,
		client:     nil,
		chainID:    nil,
	}, nil
}
