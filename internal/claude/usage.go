package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const usageEndpoint = "https://api.anthropic.com/api/oauth/usage"

// UsageChecker queries the Claude OAuth usage API for current session utilization.
type UsageChecker struct {
	httpClient *http.Client
}

func NewUsageChecker() *UsageChecker {
	return &UsageChecker{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CheckUsage returns the 5-hour window utilization (0-100).
func (u *UsageChecker) CheckUsage(ctx context.Context) (float64, error) {
	token, err := readOAuthToken()
	if err != nil {
		return 0, fmt.Errorf("read oauth token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", usageEndpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("usage API returned %d", resp.StatusCode)
	}

	var data usageResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, fmt.Errorf("decode usage: %w", err)
	}

	if data.FiveHour == nil {
		return 0, nil
	}
	return data.FiveHour.Utilization, nil
}

type usageResponse struct {
	FiveHour *usageWindow `json:"five_hour"`
}

type usageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type credentialsFile struct {
	ClaudeAIOAuth *oauthCreds `json:"claudeAiOauth"`
}

type oauthCreds struct {
	AccessToken string `json:"accessToken"`
}

func readOAuthToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("parse credentials: %w", err)
	}
	if creds.ClaudeAIOAuth == nil || creds.ClaudeAIOAuth.AccessToken == "" {
		return "", fmt.Errorf("no OAuth access token in %s", path)
	}
	return creds.ClaudeAIOAuth.AccessToken, nil
}
