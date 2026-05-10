package hostfilter

import (
	"net/url"
	"strings"
)

var ExcludedDomains = []string{
	"x.com",
	"instagram.com",
	"facebook.com",
	"yahoo.co.jp",
	"youtube.com",
	"wikipedia.org",
	"homemate-research-public.com",
	"mykoho.jp",
}

func NormalizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimRight(strings.ToLower(u.Hostname()), ".")
	return strings.TrimPrefix(host, "www.")
}

func IsExcluded(host string) bool {
	for _, d := range ExcludedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
