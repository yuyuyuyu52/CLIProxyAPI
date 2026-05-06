package helps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	npmClaudeCodeURL     = "https://registry.npmjs.org/@anthropic-ai/claude-code/latest"
	npmAnthropicSDKURL   = "https://registry.npmjs.org/@anthropic-ai/sdk/latest"
	versionFetchInterval = 24 * time.Hour
	versionFetchTimeout  = 15 * time.Second
)

var (
	dynamicVersionMu   sync.RWMutex
	dynamicCLIVersion  string
	dynamicSDKVersion  string
	versionUpdaterOnce sync.Once
)

type npmPackageLatest struct {
	Version string `json:"version"`
}

// GetDynamicCLIVersion returns the latest fetched Claude Code CLI version.
// Starts the background updater on first call. Returns empty string until
// the first successful fetch completes.
func GetDynamicCLIVersion() string {
	versionUpdaterOnce.Do(startVersionUpdater)
	dynamicVersionMu.RLock()
	defer dynamicVersionMu.RUnlock()
	return dynamicCLIVersion
}

// GetDynamicSDKVersion returns the latest fetched @anthropic-ai/sdk version.
// Returns empty string until the first successful fetch completes.
func GetDynamicSDKVersion() string {
	versionUpdaterOnce.Do(startVersionUpdater)
	dynamicVersionMu.RLock()
	defer dynamicVersionMu.RUnlock()
	return dynamicSDKVersion
}

func startVersionUpdater() {
	go func() {
		doFetchAndStoreVersions()
		ticker := time.NewTicker(versionFetchInterval)
		defer ticker.Stop()
		for range ticker.C {
			doFetchAndStoreVersions()
		}
	}()
}

func doFetchAndStoreVersions() {
	cli, sdk, err := fetchClaudeVersionsFromNPM()
	if err != nil {
		log.Debugf("claude version fetch failed, keeping previous values: %v", err)
		return
	}
	dynamicVersionMu.Lock()
	if cli != "" {
		dynamicCLIVersion = cli
	}
	if sdk != "" {
		dynamicSDKVersion = sdk
	}
	dynamicVersionMu.Unlock()
	log.Debugf("claude fingerprint versions updated: cli=%s sdk=%s", cli, sdk)
}

func fetchClaudeVersionsFromNPM() (cliVer, sdkVer string, err error) {
	client := &http.Client{Timeout: versionFetchTimeout}

	cliVer, err = fetchNPMLatestVersion(client, npmClaudeCodeURL)
	if err != nil {
		return "", "", fmt.Errorf("claude-code npm fetch: %w", err)
	}

	sdkVer, err = fetchNPMLatestVersion(client, npmAnthropicSDKURL)
	if err != nil {
		log.Debugf("anthropic-sdk npm fetch failed (non-fatal): %v", err)
		sdkVer = ""
		err = nil
	}

	return cliVer, sdkVer, nil
}

func fetchNPMLatestVersion(client *http.Client, url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var info npmPackageLatest
	if err = json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	if info.Version == "" {
		return "", fmt.Errorf("empty version field in npm response")
	}
	return info.Version, nil
}
