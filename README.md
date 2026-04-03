# Bet Bot

A Go-based automated betting bot for prediction markets on Monad.

## Features

- **Hedged Betting**: Place opposite bets (UP/DOWN) from two different wallets - one always wins
- **Candle Rush**: Bet on candle color (GREEN/RED) across multiple assets in one transaction
- **Deterministic Wallets**: Generate up to 1250 wallets from a single mnemonic for easy recovery
- **Referral System** (optional): Automatically redeem referral codes and create unique usernames for each wallet (requires `API_BASE_URL`)
- **Multi-wallet System**: Simulates organic activity with random wallet selection
- **Multi-threaded**: Run multiple concurrent betting workers
- **Both Market Types**: Supports UP/DOWN and RELATIVE predictions
- **Auto-funding**: Automatically funds wallets with USDC and MON from owner wallet
- **Encrypted Keystore**: AES-256-GCM encryption for wallet private keys at rest
- **PostgreSQL Storage**: Wallet data stored in PostgreSQL for production reliability and concurrent access

## Quick Start

```bash
# 1. Install dependencies
go mod tidy

# 2. Build all binaries
go build -o bot ./cmd/bot
go build -o candle-rush-bot ./cmd/candle-rush-bot
go build -o bot-manager ./cmd/manager
go build -o dry-run ./cmd/dry-run
go build -o sweep-all ./cmd/sweep-all
go build -o migrate-wallets ./cmd/migrate-wallets

# 3. Configure environment
cp .env.example .env
# Edit .env with your values (DATABASE_URL, OWNER_PRIVATE_KEY, etc.)

# 4. Test the flow (no transactions)
./dry-run --hedged

# 5. Run the bot
./bot --hedged
```

## Prerequisites

- **Go 1.24+**
- **PostgreSQL** — wallet data is stored in a `market_maker_wallets` table
- Set `DATABASE_URL` in your `.env` file:
  ```
  DATABASE_URL=postgres://user:password@host:port/dbname?sslmode=require
  ```
  The bot auto-creates the table on first run.

## Commands

### Bot Manager (`./bot-manager`)

Unified entry point that runs bet-bot + candle-rush-bot + periodic wallet sweep.

```bash
# Run with defaults (both bots + sweep every 8 hours)
./bot-manager --config config.yaml

# Run with custom config
./bot-manager --config config.yaml
```

Configured via `config.yaml` under the `manager:` section:
```yaml
manager:
  enabled_bots: ["bet-bot", "candle-rush-bot"]
  sweep_interval_hours: 8
```

### Main Bot (`./bot`)

```bash
# Basic run
./bot

# Dry run (no transactions)
./bot --dry-run

# Hedged betting (recommended)
./bot --hedged

# Generate wallets and run hedged
./bot --mnemonic "your phrase" --generate 100 --hedged

# Multi-threaded
./bot --threads 3 --hedged

# Full example
./bot --config config.yaml --mnemonic "your phrase" --generate 50 --threads 2 --hedged
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--dry-run` | `false` | Simulate without transactions |
| `--mnemonic` | (env) | Mnemonic for wallet generation |
| `--generate` | `0` | Number of wallets to generate |
| `--threads` | `1` | Concurrent betting workers |
| `--hedged` | `false` | Enable hedged betting |

### Candle Rush Bot (`./candle-rush-bot`)

Bet on candle colors (GREEN = price up, RED = price down) for cryptocurrency assets. Places bets on **all configured assets**, in **all configured timeframes**, for **N consecutive candles** each — all in one go per round. Tracks last bet per interval to never double-bet on the same candle.

```bash
# Run with defaults (all assets x all intervals x 5 candles)
./candle-rush-bot --private-key $PRIVATE_KEY

# Load settings from config file
./candle-rush-bot --private-key $PRIVATE_KEY --config config.yaml

# Custom assets and candle count
./candle-rush-bot --private-key $PRIVATE_KEY --assets BTC,ETH --candles 3

# Custom intervals (only 5m and 15m)
./candle-rush-bot --private-key $PRIVATE_KEY --intervals 5m,15m

# Dry run (simulation mode)
./candle-rush-bot --private-key $PRIVATE_KEY --dry-run

# Custom amount range
./candle-rush-bot --private-key $PRIVATE_KEY --min-amount 1 --max-amount 5
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file (loads `candle_rush` section) |
| `--private-key` | (env) | Wallet private key (`PRIVATE_KEY` or `OWNER_PRIVATE_KEY`) |
| `--assets` | `BTC,ETH,SOL` | Comma-separated list of assets |
| `--intervals` | `300,900,1800` | Candle intervals (`300`/`5m`, `900`/`15m`, `1800`/`30m`) |
| `--candles` | `5` | Consecutive candles to bet per interval per asset |
| `--min-amount` | `2.0` | Minimum bet amount per side (USDC) |
| `--max-amount` | `10.0` | Maximum bet amount per side (USDC) |
| `--cooldown-min` | `10` | Min cooldown between interval batches (seconds) |
| `--cooldown-max` | `30` | Max cooldown between interval batches (seconds) |
| `--halt-multiplier` | `1.0` | Halt time multiplier between rounds |
| `--min-halt` | `60` | Min halt time between rounds (seconds) |
| `--max-halt` | `60` | Max halt time between rounds (seconds) |
| `--broker` | `1` | Broker ID |
| `--dry-run` | `false` | Simulation mode (no transactions) |
| `--rpc-url` | Monad RPC | RPC endpoint |
| `--chain-id` | `10143` | Chain ID |

**Config Priority**: Flags > Environment Variables > Config File (`candle_rush` section)

#### Candle Rush Betting Strategy

Each round places bets on **every asset x every interval x N consecutive candles**:

| | BTC | ETH | SOL |
|---|---|---|---|
| **5m** (5 candles) | GREEN + RED | GREEN + RED | GREEN + RED |
| **15m** (5 candles) | GREEN + RED | GREEN + RED | GREEN + RED |
| **30m** (5 candles) | GREEN + RED | GREEN + RED | GREEN + RED |

With defaults: 3 assets x 3 intervals x 5 candles x 2 sides = **90 bets** across 3 batch transactions (one per interval). All values are configurable.

**Duplicate prevention**: The bot tracks the last candle open time per interval. After halt, it always starts from the next fresh candle — never re-bets on a candle from a previous round.

#### Candle Rush Funding Requirements

With default settings (3 assets, 3 intervals, 5 candles):
- **Per round**: 90 bets
- **Minimum per round**: `min-amount x 2 x 3 assets x 5 candles x 3 intervals = 2 x 2 x 3 x 5 x 3 = 180 USDC`
- **Recommended balance**: `500+ USDC` for extended operation

#### Candle Rush Flow

```
1. For each interval (5m, 15m, 30m):
   a. Calculate N consecutive candle open times (skipping any already bet on)
   b. Check USDC balance
   c. Ensure USDC approval for Diamond contract
   d. Build batch transaction with all combinations:
      - For each candle open time:
        - BTC GREEN + BTC RED
        - ETH GREEN + ETH RED
        - SOL GREEN + SOL RED
   e. Submit single batchPlaceBet transaction
   f. Wait for confirmation
   g. Track last candle open time for this interval
   h. Small cooldown before next interval
2. Print round stats
3. Halt between rounds (never re-bets same candle)
4. Repeat
```

### Dry Run Test (`./dry-run`)

Test the entire flow without making any transactions or API calls.

```bash
# Basic test
./dry-run

# Test hedged betting flow
./dry-run --hedged

# Test with more wallets
./dry-run --wallets 100 --hedged --verbose
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--wallets` | `5` | Number of wallets to simulate |
| `--hedged` | `false` | Show hedged betting flow |
| `--verbose` | `false` | Show detailed output |
| `--mnemonic` | (env) | Test mnemonic phrase |

### Sweep All (`./sweep-all`)

Recover USDC and MON from all wallets back to owner.

```bash
# Preview what would be swept
./sweep-all --dry-run

# Sweep both USDC and MON
./sweep-all

# Sweep only USDC
./sweep-all --mon=false

# Sweep only MON
./sweep-all --usdc=false

# Custom thresholds
./sweep-all --min-usdc 0.1 --min-mon 0.01 --reserve-gas 0.005
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--dry-run` | `false` | Show balances without sweeping |
| `--usdc` | `true` | Sweep USDC tokens |
| `--mon` | `true` | Sweep MON native tokens |
| `--min-usdc` | `0.01` | Minimum USDC to sweep |
| `--min-mon` | `0.001` | Minimum MON to sweep |
| `--reserve-gas` | `0.001` | MON to keep for gas |

### Migrate Wallets (`./migrate-wallets`)

One-time migration tool to move existing wallets from the legacy `wallets.json` file into PostgreSQL.

```bash
# Build the migration tool
go build -o migrate-wallets ./cmd/migrate-wallets

# Migrate using defaults (reads ./data/wallets.json, DATABASE_URL from .env)
./migrate-wallets

# Specify a different JSON path
./migrate-wallets --json-path /path/to/wallets.json
```

| Flag | Default | Description |
|------|---------|-------------|
| `--json-path` | `./data/wallets.json` | Path to existing wallets JSON file |

The database URL is read from `DATABASE_URL` in `.env` (or the environment). If `KEYSTORE_PASSPHRASE` is set, any plaintext private keys in the JSON will be encrypted during migration. Existing wallets in the database are skipped (`ON CONFLICT DO NOTHING`).

## How It Works

### 1. Wallet Creation (Deterministic)

Wallets are generated from a mnemonic using `Keccak256(mnemonic:index)`:

```
Index 0 -> Wallet A (0x123...)
Index 1 -> Wallet B (0x456...)
Index 2 -> Wallet C (0x789...)
...
```

- Up to **1250 wallets** can be generated
- All wallets recoverable with just the mnemonic
- Wallets stored in PostgreSQL (`market_maker_wallets` table)

### 2. Referral Setup (Optional, Per Wallet)

Referral features are **optional** and only activate when `API_BASE_URL` is set. If not configured, the bot skips all referral operations and works purely as a betting bot.

When enabled, referral is handled automatically whenever a wallet is used for betting:
1. Authenticate with API (sign challenge)
2. Redeem configured referral code (if `REFERRAL_CODE` is set and not yet redeemed)
3. Create unique referral code (e.g., `NEON-TIGER-42`) if not yet created
4. Store referral status in database

Referral is idempotent - already-redeemed/created codes are safely skipped. Wallets generated via `--generate` get their referral redeemed on first bet use.

### 3. Hedged Betting Strategy

```
Wallet 1: BTC UP   ($0.50)
Wallet 2: BTC DOWN ($0.50)
         |
One bet always wins (minus fees)
```

- Two different wallets place opposite bets
- Works with UP/DOWN markets
- For RELATIVE: HIGHEST vs LOWEST

### 4. Execution Flow

```
1. Fetch markets from Hasura
2. Select random bet
3. Get two distinct wallets
4. Fund wallets (USDC + MON)
5. Fetch fresh prices from Pyth
6. Execute primary bet (UP)
7. Execute opposite bet (DOWN)
8. Wait for cooldown
9. Repeat
```

## Database Schema

Wallet data is stored in a PostgreSQL `market_maker_wallets` table:

```sql
CREATE TABLE market_maker_wallets (
    address           TEXT PRIMARY KEY,
    private_key       TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    bet_count         INTEGER NOT NULL DEFAULT 0,
    total_volume_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_balance_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    referral_redeemed BOOLEAN NOT NULL DEFAULT FALSE,
    my_referral_code  TEXT NOT NULL DEFAULT ''
);
```

| Column | Description |
|--------|-------------|
| `address` | Ethereum wallet address (primary key) |
| `private_key` | Encrypted private key (`enc:` prefix when keystore enabled) |
| `created_at` | When the wallet was generated |
| `last_used` | Last time the wallet placed a bet |
| `bet_count` | Total number of bets placed |
| `total_volume_usd` | Cumulative bet volume in USDC |
| `last_balance_usd` | Last known USDC balance of the wallet |
| `referral_redeemed` | Whether a referral code has been redeemed |
| `my_referral_code` | The referral code this wallet created |

The table is auto-created on first connection. To migrate from the legacy `wallets.json` format, use the `migrate-wallets` command.

## Configuration

### Secrets (runtime prompts)

Sensitive values are **never required** in `.env` or config files. If not found, the bot prompts securely at startup (hidden input). Resolution order:

1. **Docker secret** — `/run/secrets/<name>` (best for containers)
2. **Environment variable** — e.g. `OWNER_PRIVATE_KEY` (works everywhere)
3. **Interactive prompt** — hidden terminal input at startup (most secure for local)

| Secret | Env Var | Docker Secret Name | When Needed |
|--------|---------|-------------------|-------------|
| Owner private key | `OWNER_PRIVATE_KEY` | `owner_private_key` | Always (bet-bot, sweep) |
| Keystore passphrase | `KEYSTORE_PASSPHRASE` | `keystore_passphrase` | Always (bet-bot, sweep) |
| Mnemonic | `MNEMONIC` | `mnemonic` | Only with `--generate` |
| Candle Rush key | `PRIVATE_KEY` | `owner_private_key` | candle-rush-bot only |
| Database URL | `DATABASE_URL` | — | Always |
| API Base URL | `API_BASE_URL` | — | Only for referral features |

### Environment Variables (`.env`)

```bash
# Database (required)
DATABASE_URL=postgres://user:password@host:5432/dbname?sslmode=require

# Non-secret config (safe to keep in .env)
HASURA_URL=https://your-hasura-endpoint/v1/graphql
HERMES_URL=https://hermes.pyth.network/v2/updates/price/latest
INITIAL_WALLET_COUNT=100
SINGLE_WALLET_MODE=false
GENERATE_NEW_WALLETS=true
COOLDOWN_SECONDS=30
THREADS=1

# Optional: Referral features (only needed if your platform supports referrals)
# API_BASE_URL=https://your-api-endpoint.com
# REFERRAL_CODE=YOUR-REFERRAL-CODE
```

### Config File (`config.yaml`)

```yaml
# Network
rpc_url: 'https://rpc.monad.xyz'
chain_id: 143

# Contracts
diamond_address: '0x928dc8afe312df45576b15b08c086c5427fd8207'
usdc_address: '0x754704Bc059F8C67012fEd69BC8A327a5aafb603'

# Wallet Management
wallets:
  db_url: '${DATABASE_URL}'
  new_wallet_probability: 0.0
  min_fund_amount_usdc: 1.0
  max_fund_amount_usdc: 3.0
  min_gas_fund_mon: 0.5
  max_gas_fund_mon: 1.0
  min_gas_balance_mon: 0.05
  min_wallets_before_reuse: 5
  generate_new_wallets: true  # false = only use existing wallets

# Betting
betting:
  min_amount_usdc: 0.1
  max_amount_usdc: 1.0
  cooldown_seconds: 30
  tolerance_bps: 500          # 5% price tolerance
  max_price_age_seconds: 10
  price_retries: 3
  broker_id: 1
  enabled_assets:
    - 'BTC'
    - 'ETH'
    - 'SOL'
  enabled_timeframes:
    - '5m'
    - '15m'
    - '30m'

# Assets
assets:
  BTC:
    address: '0x0555E30da8f98308EdB960aa94C0Db47230d2B9c'
    price_id: '0xe62df6c8b4a85fe1a67db44dc12de5db330f7ac66b72dc658afedf0f4a415b43'
  ETH:
    address: '0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242'
    price_id: '0xff61491a931112ddf1bd8147cd1b641375f79f5825126d665480874634fd0ace'
  SOL:
    address: '0xea17E5a9efEBf1477dB45082d67010E2245217f1'
    price_id: '0xef0d8b6fda2ceba41da15d4095d1da392a0d2f8ed0c6c7bc0f4cfac8c280b56d'
```

## Project Structure

```
bet-bot/
├── cmd/
│   ├── bot/main.go              # Main betting bot
│   ├── candle-rush-bot/main.go  # Candle Rush betting bot
│   ├── manager/main.go          # Unified bot manager
│   ├── dry-run/main.go          # Flow test (no transactions)
│   ├── sweep/main.go            # USDC sweep utility
│   ├── sweep-all/main.go        # USDC + MON sweep utility
│   ├── migrate-wallets/main.go  # JSON to PostgreSQL migration
│   └── test-flow/main.go        # E2E test with mocks
├── internal/
│   ├── betting/              # Strategy & executor
│   ├── bot/                  # Betting cycle engine
│   ├── candlerush/           # Candle Rush executor
│   ├── cli/                  # CLI formatting
│   ├── config/               # Configuration loading
│   ├── contracts/            # Diamond, ERC20 & CandleRush bindings
│   ├── markets/              # Hasura market fetcher
│   ├── prices/               # Pyth price fetcher
│   ├── referral/             # Referral API client
│   ├── utils/                # Name generation
│   └── wallet/               # Wallet management & encrypted keystore
├── config.yaml               # Bot configuration
├── .env                      # Environment variables
├── Dockerfile
└── docker-compose.yml
```

## Docker

### Using Docker Compose

```bash
# Build and run (dry-run mode)
docker-compose up --build

# Run in background (live mode)
docker-compose up --build -d
```

### Manual Docker

```bash
# Build
docker build -t bet-bot .

# Run dry-run
docker run --env-file .env bet-bot ./bot --dry-run

# Run live
docker run --env-file .env bet-bot ./bot --hedged
```

## Contract Addresses (Monad)

| Contract | Address |
|----------|---------|
| Diamond | `0x928dc8afe312df45576b15b08c086c5427fd8207` |
| USDC | `0x754704Bc059F8C67012fEd69BC8A327a5aafb603` |
| BTC | `0x0555E30da8f98308EdB960aa94C0Db47230d2B9c` |
| ETH | `0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242` |
| SOL | `0xea17E5a9efEBf1477dB45082d67010E2245217f1` |

## Encrypted Keystore

All wallet private keys in the database are encrypted at rest using AES-256-GCM with Argon2id key derivation. The `KEYSTORE_PASSPHRASE` is **required** — the bot will refuse to start without it.

### Providing the Passphrase

The passphrase is resolved in this order (first match wins):

1. **Docker secret** — file at `/run/secrets/keystore_passphrase`
2. **Environment variable** — `KEYSTORE_PASSPHRASE`
3. **Interactive prompt** — secure hidden input at startup

```
$ ./bot --hedged
  Enter keystore passphrase: ********
  Keystore: Enabled (keys encrypted at rest)
```

For Docker, use Docker secrets (mounted as tmpfs, invisible to `docker inspect`).

On first startup, all existing plaintext keys are automatically encrypted (one-time migration). The encrypted format uses the `enc:` prefix:

```
enc:base64(salt + nonce + ciphertext)
```

### How It Works

- **Key derivation**: Argon2id (time=3, memory=64MB, threads=4) derives a 256-bit key from your passphrase
- **Encryption**: AES-256-GCM with a unique random salt (16 bytes) and nonce (12 bytes) per key
- **Migration**: Automatic on first startup with a passphrase — encrypts all plaintext keys in the database
- **Runtime**: Keys are decrypted on-the-fly when accessed for betting or sweeping (~50-100ms per key)

### Important Notes

- **Do not lose your passphrase** — encrypted keys cannot be recovered without it
- Starting without `KEYSTORE_PASSPHRASE` will exit with an error
- Starting with a wrong passphrase will exit with a "decryption failed" error

## Security Notes

- Keep `.env` file secure - never commit to version control
- Private keys in the database are encrypted with `KEYSTORE_PASSPHRASE`
- Use a dedicated owner wallet with limited funds
- The mnemonic can recover all wallets - store it safely
- `DATABASE_URL` contains credentials - treat it as a secret

## License

MIT
