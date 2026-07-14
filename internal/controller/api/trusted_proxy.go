package api

import (
	"fmt"
	"net"
	"strings"
)

// TrustedProxySet is an explicit IP/CIDR allowlist for reverse proxies that
// are allowed to supply forwarding headers. The zero value trusts only
// loopback peers so local development keeps working without making RFC1918 or
// other remote peers implicit proxies.
type TrustedProxySet struct {
	networks []*net.IPNet
}

// ParseTrustedProxies parses a comma-separated list of exact IP addresses or
// CIDR ranges. Hostnames and malformed/empty entries are rejected: proxy trust
// must not depend on mutable DNS or an accidentally broad fallback.
func ParseTrustedProxies(raw string) (TrustedProxySet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TrustedProxySet{}, nil
	}
	parts := strings.Split(raw, ",")
	networks := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry == "" {
			return TrustedProxySet{}, fmt.Errorf("trusted proxy list contains an empty entry")
		}
		if strings.Contains(entry, "/") {
			ip, network, err := net.ParseCIDR(entry)
			if err != nil || ip == nil || network == nil {
				return TrustedProxySet{}, fmt.Errorf("invalid trusted proxy CIDR %q", entry)
			}
			// Normalize IPv4 CIDRs to a four-byte network so IPv4-mapped forms
			// cannot produce surprising Contains results.
			if ipv4 := ip.To4(); ipv4 != nil {
				ones, bits := network.Mask.Size()
				if bits != net.IPv4len*8 || ones == 0 {
					return TrustedProxySet{}, fmt.Errorf("invalid trusted proxy CIDR %q", entry)
				}
				network = &net.IPNet{IP: ipv4.Mask(network.Mask), Mask: net.CIDRMask(ones, bits)}
			} else if ones, _ := network.Mask.Size(); ones == 0 {
				return TrustedProxySet{}, fmt.Errorf("invalid trusted proxy CIDR %q", entry)
			}
			networks = append(networks, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return TrustedProxySet{}, fmt.Errorf("invalid trusted proxy IP %q", entry)
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			networks = append(networks, &net.IPNet{IP: ipv4, Mask: net.CIDRMask(32, 32)})
		} else {
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
		}
	}
	return TrustedProxySet{networks: networks}, nil
}

func (set TrustedProxySet) contains(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Loopback is always accepted for the direct localhost reverse-proxy and
	// httptest/development cases. No non-loopback address is trusted by default.
	if ip.IsLoopback() {
		return true
	}
	for _, network := range set.networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseRemoteIP(remoteAddr string) net.IP {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return net.ParseIP(strings.Trim(host, "[]"))
	}
	return net.ParseIP(strings.Trim(remoteAddr, "[]"))
}

func parseForwardedFor(value string) ([]net.IP, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, true
	}
	parts := strings.Split(value, ",")
	addresses := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, false
		}
		addresses = append(addresses, ip)
	}
	return addresses, true
}

// forwardedClientIP walks the chain from the trusted edge toward the client
// and returns the first untrusted hop. A client-supplied leftmost value cannot
// override the address appended by the first trusted proxy.
func (set TrustedProxySet) forwardedClientIP(value string) (net.IP, bool) {
	addresses, valid := parseForwardedFor(value)
	if !valid || len(addresses) == 0 {
		return nil, valid
	}
	for index := len(addresses) - 1; index >= 0; index-- {
		if !set.contains(addresses[index]) {
			return addresses[index], true
		}
	}
	// If every forwarded hop is trusted, the leftmost address is the closest
	// available identity. This is still bounded by the explicit allowlist.
	return addresses[0], true
}

func forwardedProto(value string) string {
	parts := strings.Split(value, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		proto := strings.ToLower(strings.TrimSpace(parts[index]))
		if proto == "http" || proto == "https" {
			return proto
		}
	}
	return ""
}

func (set TrustedProxySet) requestProto(r *httpRequestView) string {
	if r == nil {
		return ""
	}
	if r.tls {
		return "https"
	}
	remoteIP := parseRemoteIP(r.remoteAddr)
	if remoteIP != nil && set.contains(remoteIP) {
		return forwardedProto(r.forwardedProto)
	}
	return "http"
}

// httpRequestView keeps the forwarding parser independent from net/http's
// larger request surface and straightforward to test.
type httpRequestView struct {
	remoteAddr     string
	forwardedProto string
	tls            bool
}
