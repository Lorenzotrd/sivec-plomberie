# Bet-Bot Architecture Documentation

## Overview

Bet-Bot is an automated prediction market betting system built in Go for the Monad blockchain. It interacts with the Blinq prediction market (Diamond contract) to place hedged bets on price movements, guaranteeing one side always wins.

The system consists of two independent bots, a unified manager, and supporting infrastructure for wallet management, secret handling, and notifications.

---

## System Architecture

```
                      ┌──────────────────────┐
                      │     Bot Manager      │
                      │  cmd/manager/main.go │
                      └──────────┬───────────┘
                                 │
               ┌─────────────────┼─────────────────┐
               │                 │                  │
       ┌───────▼──────┐  ┌──────▼───────┐  ┌───────▼──────┐
       │   Bet-Bot    │  │ Candle Rush  │  │   Sweep Job  │
       │  (Hedged)    │  │    Bot       │  │ (Periodic)   │
       └───────┬──────┘  └──────┬───────┘  └──────────────┘
               │                │
       ┌───────▼────────────────▼────────┐
       │        Wallet Manager           │
       │  (Multi-wallet, Encrypted)      │
       └───────┬─────────────────────────┘
               │
       ┌───────▼─────────────────────────┐
       │      Smart Contracts            │
       │  Diamond (Prediction Market)    │
       │  USDC (ERC-20 Token)            │
       │  CandleRush (Candle Bets)       │
       └─────────────────────────────────┘
```

---

## Components

### 1. Bot Manager (`cmd/manager/main.go`)

The unified entry point that orchestrates all bots from a single process.

**Responsibilities:**
- Load configuration from `config.yaml` + `.env`
- Collect all secrets once at startup (owner key, keystore passphrase, mnemonic)
- Initialize the owner wallet and wallet manager
- Launch enabled bots as concurrent goroutines
- Run periodic sweep job to recover funds from sub-wallets

**Startup Flow:**
1. Load `.env` file and parse CLI flags (`--config`, `--dry-run`, `--generate`)
2. Load config via `config.LoadManager()` (relaxed validation, skips owner key check)
3. Resolve keystore passphrase → create `Keystore` if provided
4. Resolve owner private key → create `ownerWallet`
5. Zero out key material from config/memory
6. If bet-bot enabled: create `WalletManager`, optionally generate wallets from mnemonic
7. Setup signal handling (SIGINT/SIGTERM for graceful shutdown)
8. Launch goroutines: `runBetBot()`, `runCandleRushBot()`, `runSweep()`
9. Wait for all goroutines to complete

**Configuration (`config.yaml` → `manager:`):**
```yaml
manager:
  enabled_bots: ["bet-bot", "candle-rush-bot"]
  sweep_interval_hours: 8
```

---

### 2. Bet-Bot (Hedged Prediction Betting)

**Entry:** `cmd/bot/main.go` (standalone) or `runBetBot()` in manager

**Strategy:** Places opposite bets (UP + DOWN) from two different wallets on the same market. One always wins, generating arbitrage profit from the prediction market's payout structure.

**Betting Cycle (`internal/bot/engine.go → RunBettingCycle`):**
1. **Fetch Markets** — Query Hasura GraphQL for available UP/DOWN and Relative markets
2. **Select Bet** — `Strategy.SelectRandomBet()` picks a random eligible market, asset, timeframe, and direction
3. **Select Wallets** — In hedged mode: `WalletManager.GetTwoDistinctWallets()` returns two different wallets
4. **Create Opposite Bet** — `Strategy.CreateOppositeBet()` flips the direction for wallet 2
5. **Check & Fund Wallets** — For each wallet:
   - Check MON balance → fund gas if below `min_gas_balance_mon`
   - Check USDC balance → fund if below required bet amount + 5% buffer
6. **Execute Bets** — `Executor.ExecuteBet()` for each wallet:
   - Verify USDC balance sufficient
   - Ensure USDC approval to Diamond contract (bounded approval)
   - Fetch fresh price from Pyth oracle
   - Calculate acceptable price with tolerance (default 100 bps / 1%)
   - Call `Diamond.PredictAndBet()` or `Diamond.PredictRelativeAndBet()`
   - Wait for on-chain confirmation
7. **Record Stats** — Update wallet bet count and volume in store
8. **Cooldown** — Wait `cooldown_seconds` before next cycle

**Key Config (`config.yaml` → `betting:`):**
```yaml
betting:
  min_amount_usdc: 0.1
  max_amount_usdc: 1
  cooldown_seconds: 30
  tolerance_bps: 100        # 1% price tolerance
  max_price_age_seconds: 30 # Reject stale prices
  price_retries: 1
  broker_id: 1
  enabled_assets: [BTC, ETH, SOL]
  enabled_timeframes: [5m, 15m, 30m]
```

**Market Types:**
- **UP/DOWN** — Predict whether an asset's price goes up or down in a time period
- **Relative** — Predict which of N assets performs best in a time period

---

### 3. Candle Rush Bot (Batch Candle Color Betting)

**Entry:** `cmd/candle-rush-bot/main.go` (standalone) or `runCandleRushBot()` in manager

**Strategy:** Places GREEN (UP) + RED (DOWN) bets on every combination of assets, intervals, and consecutive candles in a single batch transaction. Uses the owner wallet directly (no sub-wallets).

**Round Flow (`internal/candlerush/executor.go`):**
1. **Choose Amount** — Random between `min_amount_usdc` and `max_amount_usdc`
2. **For Each Interval** (5m, 15m, 30m):
   - Calculate next candle open times (N consecutive candles)
   - Skip any candle open times already bet on (prevents duplicates)
   - Build batch input: GREEN + RED for each asset × each candle
   - Call `CandleRush.BatchPlaceBet()` with all inputs in one transaction
   - Track `lastBetOpenTime` per interval
3. **Cooldown** — Random delay between interval batches
4. **Halt** — Wait between rounds (configurable via halt multiplier, clamped to min/max)

**Batch Size Example (default config):**
- 3 assets × 3 intervals × 5 candles × 2 sides = **90 bets per round**
- At 0.1 USDC each = 9 USDC per round

**Key Config (`config.yaml` → `candle_rush:`):**
```yaml
candle_rush:
  assets: [BTC, ETH, SOL]
  min_amount_usdc: 0.1
  max_amount_usdc: 1.0
  intervals: [300, 900, 1800]   # 5m, 15m, 30m
  candles_per_interval: 5
  halt_time_multiplier: 1.0
  min_halt_seconds: 60
  max_halt_seconds: 60
  cooldown_min: 10
  cooldown_max: 30
  broker_id: 1
```

---

### 4. Wallet Manager (`internal/wallet/manager.go`)

Manages a pool of sub-wallets used by the bet-bot for placing bets from different addresses.

**Wallet Selection (`GetWalletForBet`):**
- **Single Wallet Mode** (`SINGLE_WALLET_MODE=true`): Always returns the owner wallet
- **Multi-wallet Mode**: Probabilistic new wallet creation vs. reuse
  - If fewer than `min_wallets_before_reuse` exist → always create new
  - Otherwise → `new_wallet_probability` chance (default 70%) to create new vs. pick random existing

**Wallet Generation:**
- **Random**: `crypto.GenerateKey()` (Go CSPRNG) for on-the-fly wallets
- **Deterministic**: `DerivePrivateKey(mnemonic, index)` from BIP39 mnemonic for reproducible wallets
- Mnemonic derivation uses `m/44'/60'/0'/0/<index>` path

**Referral System (`ensureReferral`):**
- For each new wallet, creates a unique referral code and optionally redeems a configured code
- Uses cookie-based auth flow via `referral.Client`
- Supports comma-separated referral codes (random selection per wallet)
- State tracked in wallet store (`my_referral_code`, `referral_redeemed`)

**Auto-Funding:**
- Transfers USDC from owner → sub-wallet when balance is insufficient for bet
- Transfers MON for gas when below `min_gas_balance_mon`
- Configurable min/max funding amounts

---

### 5. Wallet Store (`internal/wallet/store.go`)

Persistent JSON storage for wallet data with mutex-protected concurrent access.

**Storage:**
- File: `./data/wallets.json` (configurable via `wallets.db_path`)
- Directory permissions: `0700` (owner only)
- File permissions: `0600` (owner only)

**Stored Data Per Wallet:**
```json
{
  "address": "0x...",
  "private_key": "enc:base64(salt+nonce+ciphertext)",
  "created_at": "2024-01-01T00:00:00Z",
  "last_used": "2024-01-01T00:00:00Z",
  "bet_count": 42,
  "total_volume_usdc": 100.5,
  "referral_redeemed": true,
  "my_referral_code": "ABCDEF"
}
```

**Auto-Migration:** On first run with a passphrase, all plaintext keys are automatically encrypted and saved.

---

### 6. Keystore Encryption (`internal/wallet/keystore.go`)

Encrypts wallet private keys at rest using industry-standard cryptography.

**Algorithm:**
- **KDF:** Argon2id (memory-hard, GPU-resistant)
  - Memory: 64 MB
  - Iterations: 3
  - Threads: 4
  - Key length: 32 bytes
- **Cipher:** AES-256-GCM (authenticated encryption with associated data)

**Format:** `enc:<base64(16-byte-salt + 12-byte-nonce + ciphertext)>`

**Flow:**
1. Generate random 16-byte salt
2. Derive 256-bit key from passphrase + salt using Argon2id
3. Generate random 12-byte nonce
4. Encrypt private key hex with AES-256-GCM
5. Store as `enc:` + base64(salt || nonce || ciphertext)

---

### 7. Transaction Signer (`internal/wallet/signer.go`)

Handles wallet creation, transaction signing, and nonce management.

**Key Features:**
- **Gas Price Cap:** Capped at 10,000 Gwei to prevent draining via malicious RPC
- **Thread-Safe Nonce:** Mutex-protected local counter, lazily initialized from chain
- **Nonce Reset:** On broadcast failure, nonce re-syncs from chain on next call
- **EIP-155 Signing:** Replay-protected transactions with chain ID
- **EIP-191 Message Signing:** For off-chain auth (referral system)

**Nonce Management:**
```
First call → PendingNonceAt(chain) → store locally
Subsequent → local++
Broadcast failure → ResetNonce() → next call re-fetches from chain
```

---

### 8. Smart Contract Interfaces (`internal/contracts/`)

**Diamond (`diamond.go`):**
- `PredictAndBet()` — Place UP/DOWN prediction bet
- `PredictRelativeAndBet()` — Place relative performance bet
- Parameters include recipient, pair address, direction, period, token, amount, price, broker

**CandleRush (`candlerush.go`):**
- `BatchPlaceBet()` — Place multiple candle bets in a single transaction
- Each bet specifies asset, interval, open time, side (GREEN/RED), token, amount, broker

**ERC20 (`erc20.go`):**
- `BalanceOf()` — Check token balance
- `Allowance()` — Check spending approval
- `Approve()` — Grant spending approval (bounded to exact amount needed)
- `Transfer()` — Send tokens
- `Decimals()` — Get token decimal precision

---

### 9. Price Oracle (`internal/prices/`)

Fetches real-time asset prices from Pyth Network (Hermes gateway).

**Modes:**
- **HTTP Polling** (default): Fetches latest prices on demand
- **Streaming** (`use_streaming: true`): SSE-based real-time price feed

**Freshness Validation:**
- Prices older than `max_price_age_seconds` (default 30s) are rejected
- Retries up to `price_retries` times on stale data
- Price format: Pyth native → 1e8 fixed-point conversion

**Acceptable Price Calculation:**
- Applies `tolerance_bps` (default 100 = 1%) to current price
- For UP bets: acceptable = price × (1 + tolerance)
- For DOWN bets: acceptable = price × (1 - tolerance)

---

### 10. Secret Management (`internal/secret/`)

Resolves sensitive values with a three-tier priority system:

1. **Docker Secrets** — File at `/run/secrets/<name>` (path-traversal protected via `filepath.Base()`)
2. **Environment Variables** — Standard env var lookup
3. **Interactive Prompt** — Hidden terminal input (echo disabled via `term.ReadPassword`)

**Secrets Managed:**
| Secret | Docker Name | Env Var | Purpose |
|--------|-------------|---------|---------|
| Owner Key | `owner_private_key` | `OWNER_PRIVATE_KEY` | Wallet with funds |
| Passphrase | `keystore_passphrase` | `KEYSTORE_PASSPHRASE` | Encrypt stored keys |
| Mnemonic | `mnemonic` | `MNEMONIC` | Deterministic wallet derivation |

---

### 11. Notifications (`internal/notify/`)

Sends Slack webhook alerts for operational monitoring.

**Triggers:**
- Owner wallet USDC balance below threshold (default: 50 USDC)
- Owner wallet MON balance below threshold (default: 1 MON)
- Checked at bot startup and after each candle rush round

---

### 12. Sweep Job

Runs periodically (default: every 8 hours) when bet-bot is enabled.

**Process:**
1. Load all wallets from store
2. For each wallet:
   - Check USDC balance → if > 0.01 USDC, sweep to owner
   - Check MON balance → if > 0.001 MON, sweep to owner (keeping 0.001 MON reserve)
   - If wallet lacks gas for USDC transfer, fund gas from owner first
3. Log summary: wallets swept, amounts recovered, errors

**Standalone:** `cmd/sweep-all/main.go` runs a one-time sweep of all wallets.

---

## Security Features

### Bounded Token Approvals
USDC spending approval to the Diamond contract is limited to the exact amount needed for each operation. This limits exposure if the Diamond contract is ever compromised — an attacker cannot drain more than what was approved for the current transaction.

### Encrypted Key Storage
All wallet private keys are encrypted at rest using AES-256-GCM with Argon2id key derivation. Keys are automatically migrated from plaintext on first run with a passphrase.

### Gas Price Protection
Gas prices are capped at 10,000 Gwei to prevent wallet draining via a malicious or misbehaving RPC endpoint returning absurdly high gas prices.

### Memory Hygiene
The owner private key and mnemonic are zeroed from config structs and local variables after wallet initialization, reducing the window for memory-based attacks.

### File Permissions
Wallet store directory uses `0700` and file uses `0600` (owner-only access).

### Path Traversal Prevention
Docker secret names are sanitized via `filepath.Base()` to prevent directory traversal attacks.

### Nonce Safety
Thread-safe nonce management with mutex serialization prevents duplicate nonces across concurrent goroutines sharing the same wallet.

---

## Configuration Reference

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OWNER_PRIVATE_KEY` | — | Owner wallet private key (required) |
| `KEYSTORE_PASSPHRASE` | — | Encryption passphrase for stored keys (required) |
| `MNEMONIC` | — | BIP39 mnemonic for deterministic wallets |
| `HASURA_URL` | — | Hasura GraphQL endpoint for market data |
| `API_BASE_URL` | — | Backend API for auth/referrals (optional) |
| `SINGLE_WALLET_MODE` | `false` | Use owner wallet directly (no sub-wallets) |
| `GENERATE_NEW_WALLETS` | `true` | Create new wallets on-the-fly |
| `INITIAL_WALLET_COUNT` | `0` | Wallets to pre-generate from mnemonic |
| `THREADS` | `1` | Concurrent betting threads |
| `COOLDOWN_SECONDS` | `30` | Pause between bet cycles |
| `REFERRAL_CODE` | — | Comma-separated referral codes |
| `SLACK_WEBHOOK_URL` | — | Slack webhook for low balance alerts |
| `LOW_BALANCE_THRESHOLD_USDC` | `50` | USDC alert threshold |
| `LOW_BALANCE_THRESHOLD_MON` | `1` | MON alert threshold |

### Contract Addresses (Monad)

| Contract | Address |
|----------|---------|
| Diamond (Prediction Market) | `0x928dc8afe312df45576b15b08c086c5427fd8207` |
| USDC Token | `0x754704Bc059F8C67012fEd69BC8A327a5aafb603` |

### Asset Price IDs (Pyth)

| Asset | Contract Address | Pyth Price ID |
|-------|-----------------|---------------|
| BTC | `0x0555E30da8f98308EdB960aa94C0Db47230d2B9c` | `0xe62df6c8...` |
| ETH | `0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242` | `0xff61491a...` |
| SOL | `0xea17E5a9efEBf1477dB45082d67010E2245217f1` | `0xef0d8b6f...` |

---

## Running

### Unified Manager (Recommended)
```bash
# Run both bots with config
./bot-manager --config config.yaml

# Dry run (no transactions)
./bot-manager --config config.yaml --dry-run

# Pre-generate 50 wallets from mnemonic
./bot-manager --config config.yaml --generate 50
```

### Standalone Bots
```bash
# Bet-bot only
./bot --threads 2 --dry-run --hedged

# Candle Rush bot only
./candle-rush-bot
```

### Utilities
```bash
./sweep-all    # Sweep all sub-wallet funds to owner
./dry-run      # Test full flow without transactions
./create-user  # Create user account
```

### Docker
```bash
docker-compose up -d
```

Docker Compose runs the manager binary with secrets mounted from `/run/secrets/`.
