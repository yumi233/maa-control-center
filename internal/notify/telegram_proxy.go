package notify

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// TelegramProxyClient is an operator-configured egress, never a user-supplied
// fetch proxy. Other integrations retain their DNS/IP protections and Telegram
// still requires a verified HTTPS certificate.
func TelegramProxyClient(raw string) (*http.Client, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" {
		return nil, fmt.Errorf("invalid Telegram egress proxy")
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return nil, fmt.Errorf("Telegram egress proxy requires an explicit unicast IP and port")
	}
	tr := &http.Transport{Proxy: http.ProxyURL(u), DialContext: (&net.Dialer{Timeout: 8 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 12 * time.Second}
	return &http.Client{Timeout: 20 * time.Second, Transport: telegramOnly{tr}, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirects disabled") }}, nil
}

type telegramOnly struct{ next http.RoundTripper }

func (t telegramOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Hostname() != "api.telegram.org" || (r.URL.Port() != "" && r.URL.Port() != "443") || r.URL.User != nil || (r.Host != "" && r.Host != r.URL.Host) {
		return nil, fmt.Errorf("Telegram egress destination rejected")
	}
	return t.next.RoundTrip(r)
}
