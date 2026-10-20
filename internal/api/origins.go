package api

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Allowed origins are explicit browser origins, never host patterns or forwarded headers.
func ParseOrigins(values []string) ([]string, error) {
	if len(values) > 8 {
		return nil, fmt.Errorf("at most eight explicit origins are supported")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(u.Host, "*\\\r\n") {
			return nil, fmt.Errorf("origin must be an explicit HTTPS origin without credentials, paths or wildcards")
		}
		host := strings.ToLower(u.Hostname())
		ip := net.ParseIP(host)
		if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback()))) {
			return nil, fmt.Errorf("HTTP origins are only supported on loopback")
		}
		if u.Port() != "" {
			p, err := strconv.Atoi(u.Port())
			if err != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("invalid origin port")
			}
		}
		origin := u.Scheme + "://" + strings.ToLower(u.Host)
		if !seen[origin] {
			out = append(out, origin)
			seen[origin] = true
		}
	}
	return out, nil
}
