package http

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

var (
	cachedBuildNumber int
	buildNumberMutex  sync.RWMutex
	lastFetchTime     time.Time
	cacheDuration     = 1 * time.Hour
)

// GetLatestBuildNumber fetches the latest Discord client build number from the web client
// It caches the result for 1 hour to avoid excessive requests
func GetLatestBuildNumber() int {
	buildNumberMutex.RLock()
	if cachedBuildNumber > 0 && time.Since(lastFetchTime) < cacheDuration {
		slog.Debug("using cached build number", "build_number", cachedBuildNumber)
		buildNumberMutex.RUnlock()
		return cachedBuildNumber
	}
	buildNumberMutex.RUnlock()

	// Try to fetch the latest build number
	buildNumber, err := fetchBuildNumberFromDiscord()
	if err != nil {
		slog.Warn("failed to fetch latest build number, using fallback", "err", err, "fallback", ClientBuildNumber)
		return ClientBuildNumber
	}

	// Update cache
	buildNumberMutex.Lock()
	cachedBuildNumber = buildNumber
	lastFetchTime = time.Now()
	buildNumberMutex.Unlock()

	slog.Info("updated build number", "build_number", buildNumber)
	return buildNumber
}

func fetchBuildNumberFromDiscord() (int, error) {
	// Try multiple methods to get the build number

	// Method 1: Check the /api/v9/gateway endpoint (sometimes includes build info)
	buildNumber, err := fetchBuildNumberFromGateway()
	if err == nil && buildNumber > 0 {
		return buildNumber, nil
	}

	// Method 2: Parse from the web client's assets
	buildNumber, err = fetchBuildNumberFromAssets()
	if err == nil && buildNumber > 0 {
		return buildNumber, nil
	}

	return 0, fmt.Errorf("all methods failed to fetch build number")
}

func fetchBuildNumberFromGateway() (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Try the API info endpoint first
	req, err := http.NewRequest("GET", "https://discord.com/api/v9/gateway", nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", BrowserUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	// Look for build_number in the response
	buildNumberRegex := regexp.MustCompile(`"build_number[":]+(\d{6,})`)
	matches := buildNumberRegex.FindStringSubmatch(string(body))

	if len(matches) > 1 {
		buildNumber, err := strconv.Atoi(matches[1])
		if err == nil && buildNumber > 400000 && buildNumber < 1000000 {
			return buildNumber, nil
		}
	}

	return 0, fmt.Errorf("build number not found in gateway response")
}

func fetchBuildNumberFromAssets() (int, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// Fetch the Discord app page
	req, err := http.NewRequest("GET", "https://discord.com/app", nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", BrowserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	slog.Debug("fetched Discord app page", "size", len(body))

	// Look for build_number directly in the HTML (sometimes embedded)
	buildNumberRegex := regexp.MustCompile(`build_number[":]+(\d{6,})`)
	matches := buildNumberRegex.FindStringSubmatch(string(body))
	if len(matches) > 1 {
		buildNumber, err := strconv.Atoi(matches[1])
		if err == nil && buildNumber > 400000 && buildNumber < 1000000 {
			slog.Info("found build number in HTML", "build_number", buildNumber)
			return buildNumber, nil
		}
	}

	// Look for the assets URL pattern
	// Discord embeds build numbers in their JS asset URLs or scripts
	assetRegex := regexp.MustCompile(`/assets/[a-f0-9]+\.js`)
	assetURLs := assetRegex.FindAllString(string(body), -1)

	slog.Debug("found asset URLs", "count", len(assetURLs))

	if len(assetURLs) == 0 {
		return 0, fmt.Errorf("no asset URLs found")
	}

	// Try to fetch a few assets and look for build_number
	// Limit to first 5 to avoid too many requests
	limit := 5
	if len(assetURLs) < limit {
		limit = len(assetURLs)
	}

	for i := 0; i < limit; i++ {
		assetURL := "https://discord.com" + assetURLs[i]
		slog.Debug("checking asset", "url", assetURL)
		buildNumber, err := extractBuildNumberFromAsset(client, assetURL)
		if err == nil && buildNumber > 0 {
			return buildNumber, nil
		}
	}

	return 0, fmt.Errorf("build number not found in assets")
}

func extractBuildNumberFromAsset(client *http.Client, assetURL string) (int, error) {
	req, err := http.NewRequest("GET", assetURL, nil)
	if err != nil {
		return 0, err
	}

	req.Header.Set("User-Agent", BrowserUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	// Look for build_number pattern in the JS
	buildNumberRegex := regexp.MustCompile(`build_number[":]+(\d{6,})`)
	matches := buildNumberRegex.FindStringSubmatch(string(body))

	if len(matches) > 1 {
		buildNumber, err := strconv.Atoi(matches[1])
		if err == nil && buildNumber > 400000 && buildNumber < 1000000 {
			return buildNumber, nil
		}
	}

	return 0, fmt.Errorf("build number pattern not found")
}
