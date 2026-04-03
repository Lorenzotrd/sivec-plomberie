package referral

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

type MessageSigner interface {
	SignMessage(message string) (string, error)
}

type Client struct {
	apiBaseURL string
	httpClient *http.Client
}

func NewClient(apiBaseURL string) *Client {
	// Use cookie jar to support cookie-based auth (matching betme-interface's credentials: "include")
	jar, _ := cookiejar.New(nil)
	return &Client{
		apiBaseURL: strings.TrimRight(apiBaseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}
}

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
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type CreateCodeRequest struct {
	WalletAddress string `json:"wallet_address"`
	Code          string `json:"code"`
}

type CreateCodeResponse struct {
	Code        string `json:"code"`
	OwnerWallet string `json:"owner_wallet"`
	IsActive    bool   `json:"is_active"`
}

type RedeemRequest struct {
	WalletAddress string `json:"wallet_address"`
	Code          string `json:"code"`
}

type RedeemResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// EnsureReferral performs the full referral flow for a wallet in one auth session:
// 1. Challenge + Sign + Login
// 2. Sync points (fire-and-forget)
// 3. Create own referral code (if needed)
// 4. Redeem referrer's code (if needed)
// This matches the betme-interface flow and avoids multiple auth round-trips.
func (c *Client) EnsureReferral(signer MessageSigner, walletAddress string, createCode string, redeemCode string) (codeCreated bool, codeRedeemed bool, err error) {
	// Nothing to do
	if createCode == "" && redeemCode == "" {
		return false, false, nil
	}

	// 1. Get Auth Challenge
	challenge, err := c.getChallenge(walletAddress)
	if err != nil {
		return false, false, fmt.Errorf("failed to get auth challenge: %w", err)
	}

	// 2. Sign Message
	signature, err := signer.SignMessage(challenge.Message)
	if err != nil {
		return false, false, fmt.Errorf("failed to sign auth message: %w", err)
	}

	// 3. Login (cookies are stored automatically via cookie jar)
	accessToken, err := c.login(walletAddress, challenge.Nonce, signature)
	if err != nil {
		return false, false, fmt.Errorf("failed to login: %w", err)
	}

	// 4. Sync points (fire-and-forget with timeout, matching betme-interface behavior)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.syncPoints(ctx, accessToken)
	}()

	// 5. Create own referral code first (matching betme-interface order: create before redeem)
	if createCode != "" {
		if err := c.create(walletAddress, createCode, accessToken); err != nil {
			return false, false, fmt.Errorf("failed to create code: %w", err)
		}
		codeCreated = true
	}

	// 6. Redeem referrer's code
	if redeemCode != "" {
		if err := c.redeem(walletAddress, redeemCode, accessToken); err != nil {
			return codeCreated, false, fmt.Errorf("failed to redeem code: %w", err)
		}
		codeRedeemed = true
	}

	return codeCreated, codeRedeemed, nil
}

// CreateCode creates a new referral code for the wallet
func (c *Client) CreateCode(signer MessageSigner, walletAddress, code string) error {
	challenge, err := c.getChallenge(walletAddress)
	if err != nil {
		return fmt.Errorf("failed to get auth challenge: %w", err)
	}

	signature, err := signer.SignMessage(challenge.Message)
	if err != nil {
		return fmt.Errorf("failed to sign auth message: %w", err)
	}

	accessToken, err := c.login(walletAddress, challenge.Nonce, signature)
	if err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.syncPoints(ctx, accessToken)
	}()

	if err := c.create(walletAddress, code, accessToken); err != nil {
		return fmt.Errorf("failed to create code: %w", err)
	}

	return nil
}

// RedeemCode attempts to redeem a referral code for a given wallet
func (c *Client) RedeemCode(signer MessageSigner, walletAddress, code string) error {
	challenge, err := c.getChallenge(walletAddress)
	if err != nil {
		return fmt.Errorf("failed to get auth challenge: %w", err)
	}

	signature, err := signer.SignMessage(challenge.Message)
	if err != nil {
		return fmt.Errorf("failed to sign auth message: %w", err)
	}

	accessToken, err := c.login(walletAddress, challenge.Nonce, signature)
	if err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.syncPoints(ctx, accessToken)
	}()

	if err := c.redeem(walletAddress, code, accessToken); err != nil {
		return fmt.Errorf("failed to redeem code: %w", err)
	}

	return nil
}

func (c *Client) getChallenge(address string) (*ChallengeResponse, error) {
	url := fmt.Sprintf("%s/auth-service/auth/challenge?address=%s", c.apiBaseURL, strings.ToLower(address))
	resp, err := c.httpClient.Get(url)
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

func (c *Client) login(address, nonce, signature string) (string, error) {
	url := fmt.Sprintf("%s/auth-service/auth/login", c.apiBaseURL)
	reqBody := LoginRequest{
		Address:   strings.ToLower(address),
		Nonce:     nonce,
		Signature: signature,
	}

	jsonBody, _ := json.Marshal(reqBody)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
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

// syncPoints triggers a points sync after login with a context for timeout control.
// This matches the betme-interface behavior: POST /reward-service/api/rewards/sync-points
func (c *Client) syncPoints(ctx context.Context, accessToken string) {
	url := fmt.Sprintf("%s/reward-service/api/rewards/sync-points", c.apiBaseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return
	}
	if accessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func (c *Client) create(address, code, accessToken string) error {
	url := fmt.Sprintf("%s/referral-service/api/referral/codes", c.apiBaseURL)
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		msg := parseErrorMessage(body)

		// Ignore 409 Conflict or "already exists" errors
		if resp.StatusCode == 409 || strings.Contains(strings.ToLower(msg), "already") || strings.Contains(strings.ToLower(msg), "code_already_exists") {
			return nil
		}

		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	return nil
}

func (c *Client) redeem(address, code, accessToken string) error {
	url := fmt.Sprintf("%s/referral-service/api/referral/redeem", c.apiBaseURL)
	reqBody := RedeemRequest{
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := parseErrorMessage(body)

		// Ignore "already redeemed" errors
		if strings.Contains(strings.ToLower(msg), "already") {
			return nil
		}

		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	return nil
}

// parseErrorMessage extracts a human-readable error message from API response body
func parseErrorMessage(body []byte) string {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return string(body)
	}
	// Check common error field names
	for _, key := range []string{"message", "error", "detail"} {
		if m, ok := result[key].(string); ok && m != "" {
			return m
		}
	}
	return string(body)
}
