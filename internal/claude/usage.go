package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sina-geth/geth-triage/internal/analyzer"
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

// CheckUsage returns the 5-hour window utilization and reset time.
func (u *UsageChecker) CheckUsage(ctx context.Context) (*analyzer.UsageStatus, error) {
	token, err := readOAuthToken()
	if err != nil {
		return nil, fmt.Errorf("read oauth token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", usageEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage API returned %d", resp.StatusCode)
	}

	var data usageResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode usage: %w", err)
	}

	status := &analyzer.UsageStatus{}
	if data.FiveHour != nil {
		status.Utilization = data.FiveHour.Utilization
		if data.FiveHour.ResetsAt != "" {
			status.ResetsAt, _ = time.Parse(time.RFC3339, data.FiveHour.ResetsAt)
		}
	}
	return status, nil
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
