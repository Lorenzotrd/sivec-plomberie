package main

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Default API base URL (can be overridden with -api flag or API_BASE_URL env var)
var defaultAPIBaseURL = os.Getenv("API_BASE_URL")

var (
	adjectives = []string{
		"Cyber", "Neon", "Quantum", "Sonic", "Mega", "Hyper", "Ultra",
		"Swift", "Rapid", "Turbo", "Flash", "Zero", "Alpha", "Omega",
		"Brave", "Calm", "Cool", "Eager", "Fair", "Grand", "Happy",
		"Jolly", "Kind", "Lively", "Mighty", "Noble", "Proud", "Quick",
		"Royal", "Sharp", "Smart", "Strong", "Super", "Sweet", "Tough",
		"Wise", "Wild", "Young", "Zesty", "Vivid", "Lucky", "Magic",
	}

	nouns = []string{
		"Goku", "Luffy", "Naruto", "Saitama", "Deku", "Asta", "Tanjiro",
		"Eren", "Levi", "Zoro", "Sanji", "Nami", "Kira", "L", "Rem",
		"Asuna", "Kirito", "Gojo", "Itadori", "Sukuna", "Denji", "Power",
		"Tiger", "Lion", "Wolf", "Bear", "Eagle", "Hawk", "Fox", "Cat",
		"Dog", "Panda", "Koala", "Shark", "Whale", "Dolphin", "Snake",
		"Dragon", "Phoenix", "Griffin", "Hydra", "Titan", "Giant",
		"Droid", "Mech", "Bot", "Cyborg", "Clone", "Drone", "Pilot",
		"Racer", "Walker", "Runner", "Surfer", "Hunter", "Warrior",
		"Ninja", "Samurai", "Knight", "Wizard", "Mage", "Sage", "Hero",
	}
)

// Wallet for signing messages
type Wallet struct {
	Address    common.Address
	PrivateKey *ecdsa.PrivateKey
}

// API request/response types
type ChallengeResponse struct {
	Message   string `json:"message"`
	Nonce     string `json:"nonce"`
	ExpiresAt string `json:"expires_at"`
}

type LoginRequest struct {
	Address   string `json:"address"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

type LoginResponse struct {
	AccessToken string `json:"access_token"`
}

type CreateCodeRequest struct {
	WalletAddress string `json:"wallet_address"`
	Code          string `json:"code"`
}

func main() {
	rand.Seed(time.Now().UnixNano())

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <private_key> [username] [api_base_url]")
		fmt.Println("")
		fmt.Println("Arguments:")
		fmt.Println("  private_key    - Wallet private key (with or without 0x prefix)")
		fmt.Println("  username       - (Optional) Custom username, otherwise random")
		fmt.Println("  api_base_url   - (Optional) API base URL, defaults to API_BASE_URL env var")
		fmt.Println("")
		fmt.Println("Example:")
		fmt.Println("  go run main.go 0xabcd1234...")
		fmt.Println("  go run main.go 0xabcd1234... MyUsername")
		fmt.Println("  go run main.go 0xabcd1234... MyUsername https://your-api.example.com")
		os.Exit(1)
	}

	privateKeyHex := os.Args[1]
	customUsername := ""
	apiBaseURL := defaultAPIBaseURL

	// Parse optional arguments
	if len(os.Args) >= 3 {
		arg := os.Args[2]
		if strings.HasPrefix(arg, "http") {
			apiBaseURL = strings.TrimRight(arg, "/")
		} else {
			customUsername = arg
		}
	}
	if len(os.Args) >= 4 {
		apiBaseURL = strings.TrimRight(os.Args[3], "/")
	}

	if apiBaseURL == "" {
		fmt.Println("Error: API_BASE_URL environment variable is not set and no URL was provided as argument")
		os.Exit(1)
	}

	// Create wallet from private key
	wallet, err := newWallet(privateKeyHex)
	if err != nil {
		fmt.Printf("Error: Failed to create wallet: %v\n", err)
		os.Exit(1)
	}

	// Generate random name or use custom
	username := customUsername
	if username == "" {
		username = generateRandomName()
	}
	code := strings.ToUpper(username)

	fmt.Println("=========================================")
	fmt.Printf("Address:  %s\n", wallet.Address.Hex())
	fmt.Printf("Username: %s\n", username)
	fmt.Printf("Code:     %s\n", code)
	fmt.Printf("API:      %s\n", apiBaseURL)
	fmt.Println("=========================================")
	fmt.Println("")

	// Step 1: Get auth challenge
	fmt.Println("[1/4] Getting auth challenge...")
	challenge, err := getChallenge(apiBaseURL, wallet.Address.Hex())
	if err != nil {
		fmt.Printf("Error: Failed to get challenge: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("      Challenge received (nonce: %s)\n", challenge.Nonce)

	// Step 2: Sign message
	fmt.Println("[2/4] Signing message...")
	signature, err := wallet.SignMessage(challenge.Message)
	if err != nil {
		fmt.Printf("Error: Failed to sign message: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("      Signature: %s...%s\n", signature[:10], signature[len(signature)-6:])

	// Step 3: Login
	fmt.Println("[3/4] Logging in...")
	accessToken, err := login(apiBaseURL, wallet.Address.Hex(), challenge.Nonce, signature)
	if err != nil {
		fmt.Printf("Error: Failed to login: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("      Access token received\n")

	// Step 4: Create referral code (username)
	fmt.Println("[4/4] Creating user with referral code...")
	if err := createCode(apiBaseURL, wallet.Address.Hex(), code, accessToken); err != nil {
		fmt.Printf("Error: Failed to create code: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("")
	fmt.Println("=========================================")
	fmt.Println("SUCCESS!")
	fmt.Printf("User created with referral code: %s\n", code)
	fmt.Println("=========================================")
}

func newWallet(privateKeyHex string) (*Wallet, error) {
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

	return &Wallet{
		Address:    address,
		PrivateKey: privateKey,
	}, nil
}

func (w *Wallet) SignMessage(message string) (string, error) {
	data := []byte(message)
	hash := crypto.Keccak256Hash(
		[]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(data), data)),
	)

	signature, err := crypto.Sign(hash.Bytes(), w.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	// Adjust V value (27/28 for standard RPC)
	if len(signature) == 65 {
		signature[64] += 27
	}

	return fmt.Sprintf("0x%x", signature), nil
}

func generateRandomName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s%s", adj, noun)
}

func getChallenge(apiBaseURL, address string) (*ChallengeResponse, error) {
	url := fmt.Sprintf("%s/auth-service/auth/challenge?address=%s", apiBaseURL, strings.ToLower(address))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var challenge ChallengeResponse
	if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
		return nil, err
	}
	return &challenge, nil
}

func login(apiBaseURL, address, nonce, signature string) (string, error) {
	url := fmt.Sprintf("%s/auth-service/auth/login", apiBaseURL)
	reqBody := LoginRequest{
		Address:   strings.ToLower(address),
		Nonce:     nonce,
		Signature: signature,
	}

	jsonBody, _ := json.Marshal(reqBody)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var loginResp LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", err
	}
	return loginResp.AccessToken, nil
}

func createCode(apiBaseURL, address, code, accessToken string) error {
	url := fmt.Sprintf("%s/referral-service/api/referral/codes", apiBaseURL)
	reqBody := CreateCodeRequest{
		WalletAddress: strings.ToLower(address),
		Code:          code,
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		var result map[string]interface{}
		json.Unmarshal(body, &result)
		msg := string(body)
		if m, ok := result["message"].(string); ok {
			msg = m
		}

		// Ignore 409 Conflict if already exists
		if resp.StatusCode == 409 || (resp.StatusCode == 400 && strings.Contains(strings.ToLower(msg), "already")) {
			fmt.Printf("      Note: Code already exists (this is OK)\n")
			return nil
		}

		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	return nil
}
