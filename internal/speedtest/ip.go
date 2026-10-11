package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var defaultHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// FetchPublicIP retrieves the public IP address from api.ipify.org.
// Failures are non-fatal and return an error.
func FetchPublicIP(ctx context.Context, client *http.Client) (string, error) {
	if client == nil {
		client = defaultHTTPClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ipify returned unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return "", errors.New("empty IP returned by ipify")
	}

	return ip, nil
}
