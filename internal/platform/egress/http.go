// Package egress provides destination-bound outbound HTTPS transports.
package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

// NewClient permits one HTTPS origin, pins DNS answers and never follows redirects.
// fixture allows only literal loopback destinations for controlled local tests.
func NewClient(origin, caFile string, fixture bool) (*http.Client, error) {
	return newClient(origin, caFile, fixture, false)
}

// NewInternalClient permits an explicitly trusted private service origin with a mounted CA.
func NewInternalClient(origin, caFile string) (*http.Client, error) {
	if caFile == "" {
		return nil, errors.New("private service CA is required")
	}
	return newClient(origin, caFile, false, true)
}
func newClient(origin, caFile string, fixture, private bool) (*http.Client, error) {
	endpoint, err := url.Parse(origin)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("invalid egress origin")
	}
	hostname := endpoint.Hostname()
	port := endpoint.Port()
	if port == "" {
		port = "443"
	}
	if fixture {
		ip, err := netip.ParseAddr(hostname)
		if err != nil || !ip.IsLoopback() {
			return nil, errors.New("fixture origin must be literal loopback")
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: hostname}
	if caFile != "" {
		raw, err := os.ReadFile(caFile) // #nosec G304 -- explicit operator-selected trust root.
		if err != nil {
			return nil, errors.New("read egress CA")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(raw) {
			return nil, errors.New("invalid egress CA")
		}
		tlsConfig.RootCAs = roots
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		MaxConnsPerHost:       4,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, requestedPort, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, hostname) || requestedPort != port {
				return nil, errors.New("egress destination denied")
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("egress DNS unavailable")
			}
			for _, address := range addresses {
				address = address.Unmap()
				if fixture {
					if !address.IsLoopback() {
						return nil, errors.New("fixture destination denied")
					}
				} else if !private && (!address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || special(address)) {
					return nil, errors.New("egress address denied")
				}
			}
			dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
			for _, address := range addresses {
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
				if err == nil {
					return connection, nil
				}
			}
			return nil, errors.New("egress connection unavailable")
		}}

	return &http.Client{
		Transport: &originTransport{origin: endpoint, transport: transport},
		Timeout:   2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("egress redirect denied")
		},
	}, nil
}
func special(address netip.Addr) bool {
	for _, value := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2002::/16"} {
		if netip.MustParsePrefix(value).Contains(address) {
			return true
		}
	}
	return false
}

type originTransport struct {
	origin    *url.URL
	transport *http.Transport
}

func (transport *originTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != transport.origin.Scheme || request.URL.Host != transport.origin.Host || request.URL.User != nil {
		return nil, errors.New("egress origin denied")
	}
	return transport.transport.RoundTrip(request)
}
func (transport *originTransport) CloseIdleConnections() { transport.transport.CloseIdleConnections() }
