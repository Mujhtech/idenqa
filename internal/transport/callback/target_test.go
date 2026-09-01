package callback

import (
	"context"
	"net/netip"
	"testing"
)

type fixedResolver struct{ values []netip.Addr }

func (resolver fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.values...), nil
}

func TestResolveTargetRejectsInternalNetworks(t *testing.T) {
	unsafe := []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "203.0.113.10", "::1", "2001:db8::1"}
	for _, encoded := range unsafe {
		if _, err := ResolveTarget(t.Context(), fixedResolver{[]netip.Addr{netip.MustParseAddr(encoded)}}, "https://tenant.example/hook"); err == nil {
			t.Fatalf("accepted %s", encoded)
		}
	}
	target, err := ResolveTarget(t.Context(), fixedResolver{[]netip.Addr{netip.MustParseAddr("8.8.8.8")}}, "https://tenant.example/hook")
	if err != nil || target.URL.Hostname() != "tenant.example" {
		t.Fatalf("target=%#v err=%v", target, err)
	}
}
