package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lg-jp/jp-municipality-domains/internal/hostfilter"
	"github.com/lg-jp/jp-municipality-domains/internal/municipality"
	"github.com/lg-jp/jp-municipality-domains/internal/searxng"
)

const (
	searxngBaseURL = "http://127.0.0.1:8080"
	resultLimit    = 3
	requestDelay   = time.Second
)

//go:embed data.json
var dataJSON []byte

func pickTopHits(results []string, limit int) []string {
	top := make([]string, 0, limit)
	for _, r := range results {
		host := hostfilter.NormalizeHost(r)
		if host == "" || hostfilter.IsExcluded(host) {
			continue
		}
		top = append(top, r)
		if len(top) >= limit {
			break
		}
	}
	return top
}

func containsHost(urls []string, expectedHost string) bool {
	if expectedHost == "" {
		return false
	}
	for _, u := range urls {
		if hostfilter.NormalizeHost(u) == expectedHost {
			return true
		}
	}
	return false
}

func run(logger *slog.Logger) error {
	client, err := searxng.NewClient(searxngBaseURL)
	if err != nil {
		return fmt.Errorf("init searxng client: %w", err)
	}

	var records []municipality.Municipality
	if err := json.Unmarshal(dataJSON, &records); err != nil {
		return fmt.Errorf("unmarshal embedded data.json: %w", err)
	}

	var failed bool
	for _, m := range records {
		if m.URL == "" {
			continue
		}

		query := m.BuildQuery()
		hits, err := client.Search(query)
		if err != nil {
			failed = true
			logger.Error("search failed",
				"municipality_code", m.Code,
				"query", query,
				"error", err.Error(),
			)
			time.Sleep(requestDelay)
			continue
		}

		top := pickTopHits(hits, resultLimit)
		if !containsHost(top, hostfilter.NormalizeHost(m.URL)) {
			failed = true
			logger.Warn("mismatch",
				"municipality_code", m.Code,
				"query", query,
				"registered", m.URL,
				"top_hits", top,
			)
		}

		time.Sleep(requestDelay)
	}

	if failed {
		return errors.New("url monitor detected mismatch or search error")
	}
	return nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err.Error())
		os.Exit(1)
	}
}
