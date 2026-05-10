package searxng

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const requestTimeout = 90 * time.Second

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

func NewClient(rawBaseURL string) (*Client, error) {
	base, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid base url: %q", rawBaseURL)
	}
	return &Client{
		baseURL: base,
		http:    &http.Client{Timeout: requestTimeout},
	}, nil
}

func (c *Client) Search(query string) ([]string, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/search"
	endpoint.RawQuery = url.Values{
		"q":        {query},
		"format":   {"json"},
		"engines":  {"google"},
		"language": {"ja"},
	}.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("searxng %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode searxng response: %w", err)
	}

	urls := make([]string, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.URL != "" {
			urls = append(urls, r.URL)
		}
	}
	return urls, nil
}
