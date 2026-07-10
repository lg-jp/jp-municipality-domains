package main

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed data.json
var dataJSON []byte

const (
	browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	defaultWorkers       = 32
	githubActionsWorkers = 12 // avoid mass rate-limit / flaky failures on runners
	reqTimeout           = 45 * time.Second
	maxRetries           = 5
	baseBackoff          = 800 * time.Millisecond
	maxRedirects         = 10
	bodyReadLimit        = 256 * 1024
)

// errEdgeBlocked marks a response a CDN produced itself to reject us, rather
// than one it proxied from the origin. The domain is alive; our source IP is
// simply not welcome, so this is reported but never counted as a failure.
var errEdgeBlocked = errors.New("blocked at CDN edge")

type record struct {
	URL string `json:"url"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)

	nWorkers := resolveWorkerCount()

	recs, err := unmarshalRecords(dataJSON)
	if err != nil {
		logger.Error("unmarshal embedded data.json", "error", err)
		os.Exit(1)
	}

	var failCount atomic.Int64
	var blockedCount atomic.Int64
	var checked atomic.Int64
	sem := make(chan struct{}, nWorkers)
	var wg sync.WaitGroup
	ctx := context.Background()

	for _, r := range recs {
		u := strings.TrimSpace(r.URL)
		if u == "" {
			continue
		}
		checked.Add(1)
		wg.Add(1)
		go func(raw string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			switch err := monitorURL(ctx, logger, raw); {
			case err == nil:
			case errors.Is(err, errEdgeBlocked):
				blockedCount.Add(1)
				logger.Warn("URL blocked at CDN edge; host is reachable, not counted as a failure", "url", raw, "error", err)
			default:
				failCount.Add(1)
				logger.Error("URL check failed", "url", raw, "error", err)
			}
		}(u)
	}
	wg.Wait()

	if n := blockedCount.Load(); n > 0 {
		logger.Warn("some URLs were blocked at a CDN edge", "blocked", n, "checked", checked.Load())
	}
	if n := failCount.Load(); n > 0 {
		logger.Error("finished with failures", "failed", n, "checked", checked.Load())
		os.Exit(1)
	}
}

func resolveWorkerCount() int {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return githubActionsWorkers
	}
	return defaultWorkers
}

func unmarshalRecords(b []byte) ([]record, error) {
	var recs []record
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, err
	}
	return recs, nil
}

func monitorURL(ctx context.Context, log *slog.Logger, rawURL string) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			n := attempt
			wait := time.Duration(float64(baseBackoff) * float64(n*(n+1)) / 2)
			log.Info("retrying after backoff", "url", rawURL, "attempt", attempt+1, "wait", wait)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}

		attemptLog := log.With("url", rawURL, "attempt", attempt+1)
		err := fetchOnce(ctx, attemptLog, rawURL)
		if err == nil {
			return nil
		}
		// The edge decides on our source IP, so every retry repeats the block.
		if errors.Is(err, errEdgeBlocked) {
			return err
		}
		lastErr = err
		if !isIgnorableTLSError(err) {
			attemptLog.Warn("request attempt failed", "error", err)
		}
	}
	return fmt.Errorf("after %d attempts: %w", maxRetries, lastErr)
}

func fetchOnce(ctx context.Context, log *slog.Logger, rawURL string) error {
	parsedStart, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("bad URL: %w", err)
	}
	if parsedStart.Host == "" {
		return fmt.Errorf("bad URL: missing host")
	}
	startHost := deriveCanonicalHost(parsedStart)
	startRef := *parsedStart
	startRef.Fragment = ""
	startURL := startRef.String()

	checkRedirect := func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		prev := via[len(via)-1].URL
		fromHost := deriveCanonicalHost(prev)
		toHost := deriveCanonicalHost(req.URL)

		line := log.With(
			"redirect_from", prev.String(),
			"redirect_to", req.URL.String(),
			"code_from_host", prev.Host,
			"code_to_host", req.URL.Host,
		)

		switch {
		case fromHost != "" && toHost != "" && fromHost != toHost:
			line.Warn("redirect: host changed (domain may have moved)", "from_host", fromHost, "to_host", toHost)
		case fromHost != toHost:
			line.Info("redirect", "from_host", fromHost, "to_host", toHost)
		default:
			line.Info("redirect (same host)", "host", toHost)
		}

		if startHost != "" && toHost != "" && toHost != startHost {
			line.With("original_start", startURL).Info("redirect chain left original entry host", "entry_host", startHost, "current_host", toHost)
		}
		return nil
	}

	client := &http.Client{
		Timeout:       reqTimeout,
		CheckRedirect: checkRedirect,
		Transport:     tlsIgnoringTransport(),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	applyBrowserHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		if isIgnorableTLSError(err) {
			return nil
		}
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, bodyReadLimit)); err != nil {
		return err
	}
	if edge := edgeBlockSource(resp); edge != "" {
		return fmt.Errorf("HTTP %d from %s: %w", resp.StatusCode, edge, errEdgeBlocked)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return nil
}

// edgeBlockSource names the CDN that rejected the request, or "" if the
// response came from the origin. A CDN passes the origin's Server header
// through on a proxied response, so a CDN naming itself there alongside a 403
// means the request never reached the site. Add other CDNs here once a real
// response from one has been observed.
func edgeBlockSource(resp *http.Response) string {
	if resp.StatusCode != http.StatusForbidden {
		return ""
	}
	server := strings.ToLower(resp.Header.Get("Server"))
	xCache := strings.ToLower(resp.Header.Get("X-Cache"))
	if server == "cloudfront" || strings.Contains(xCache, "error from cloudfront") {
		return "CloudFront"
	}
	return ""
}

func tlsIgnoringTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	return transport
}

func isIgnorableTLSError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate")
}

func applyBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ja,en-US;q=0.9,en;q=0.8")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")
}

func deriveCanonicalHost(u *url.URL) string {
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
}
