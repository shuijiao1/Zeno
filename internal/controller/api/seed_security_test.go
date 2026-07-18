package api

import (
	"net/netip"
	"strings"
	"testing"
)

func TestDefaultPreviewProbeTargetsUseDocumentationOnlyData(t *testing.T) {
	documentationPrefixes := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, target := range DefaultPreviewProbeTargets() {
		address, err := netip.ParseAddr(target.Address)
		if err != nil {
			t.Fatalf("preview target %q address is not an IP: %v", target.ID, err)
		}
		allowed := false
		for _, prefix := range documentationPrefixes {
			allowed = allowed || prefix.Contains(address)
		}
		if !allowed {
			t.Errorf("preview target %q uses non-documentation address", target.ID)
		}
		if !strings.HasPrefix(target.Name, "Example ") {
			t.Errorf("preview target %q does not use an explicitly fictional name", target.ID)
		}
	}
}
