// Package callback owns outbound webhook HTTP transport.
package callback

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// ErrUnsafeTarget means an endpoint could reach a forbidden network target.
var ErrUnsafeTarget = errors.New("callback: unsafe target")

// Resolver is injected so DNS answers can be tested and pinned per attempt.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// Target is a validated HTTPS endpoint with its attempt-pinned public addresses.
type Target struct {
	URL       *url.URL
	Addresses []netip.Addr
}

// ResolveTarget validates an HTTPS URL and pins all public DNS answers.
func ResolveTarget(ctx context.Context, resolver Resolver, encoded string) (Target, error) {
	if resolver == nil || len(encoded) == 0 || len(encoded) > 2048 {
		return Target{}, ErrUnsafeTarget
	}
	parsed, err := url.Parse(encoded)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return Target{}, ErrUnsafeTarget
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return Target{}, ErrUnsafeTarget
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil {
		return Target{}, fmt.Errorf("resolve callback target: %w", err)
	}
	if len(addresses) == 0 || len(addresses) > 16 {
		return Target{}, ErrUnsafeTarget
	}
	result := make([]netip.Addr, len(addresses))
	for index, address := range addresses {
		address = address.Unmap()
		if unsafeAddress(address) {
			return Target{}, ErrUnsafeTarget
		}
		result[index] = address
	}
	copyURL := *parsed
	return Target{URL: &copyURL, Addresses: result}, nil
}

func unsafeAddress(address netip.Addr) bool {
	if !address.IsValid() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
	} {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// DialContext pins one validated DNS answer for the complete connection.
func (target Target) DialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if dialer == nil || target.URL == nil || len(target.Addresses) == 0 || !strings.EqualFold(hostOnly(address), target.URL.Hostname()) {
			return nil, ErrUnsafeTarget
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(target.Addresses[0].String(), "443"))
	}
}

func hostOnly(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}
