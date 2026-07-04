package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NetworkIdentity contains auto-discovered public network metadata for a node.
// Empty fields mean discovery failed or the host does not have that address family.
type NetworkIdentity struct {
	PublicIPv4  string
	PublicIPv6  string
	CountryCode string
}

func (identity NetworkIdentity) empty() bool {
	return identity.PublicIPv4 == "" && identity.PublicIPv6 == "" && identity.CountryCode == ""
}

// NetworkIdentityDiscoverer discovers public IPv4/IPv6 and a country code using
// tokenless, replaceable HTTP providers. Discovery is best-effort: failures return
// partial or empty identity rather than an error so Agent reporting can continue.
type NetworkIdentityDiscoverer struct {
	Client         *http.Client
	IPv4URL        string
	IPv6URL        string
	GeoIPURLFormat string
}

type ipifyResponse struct {
	IP string `json:"ip"`
}

type geoIPResponse struct {
	Success     bool   `json:"success"`
	CountryCode string `json:"country_code"`
	IP          string `json:"ip"`
}

func NewNetworkIdentityDiscoverer() *NetworkIdentityDiscoverer {
	return &NetworkIdentityDiscoverer{
		Client:         &http.Client{Timeout: 2 * time.Second},
		IPv4URL:        "https://api.ipify.org?format=json",
		IPv6URL:        "https://api6.ipify.org?format=json",
		GeoIPURLFormat: "https://ipwho.is/%s?fields=success,country_code,ip",
	}
}

func (d *NetworkIdentityDiscoverer) Discover(ctx context.Context) NetworkIdentity {
	if d == nil {
		return NetworkIdentity{}
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	identity := NetworkIdentity{}
	identity.PublicIPv4 = d.fetchPublicIP(ctx, client, d.IPv4URL, 4)
	identity.PublicIPv6 = d.fetchPublicIP(ctx, client, d.IPv6URL, 6)
	if identity.PublicIPv4 != "" {
		identity.CountryCode = d.fetchCountryCode(ctx, client, identity.PublicIPv4)
	}
	if identity.CountryCode == "" && identity.PublicIPv6 != "" {
		identity.CountryCode = d.fetchCountryCode(ctx, client, identity.PublicIPv6)
	}
	return identity
}

func (d *NetworkIdentityDiscoverer) fetchPublicIP(ctx context.Context, client *http.Client, url string, family int) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	var response ipifyResponse
	if err := d.fetchJSON(ctx, client, url, &response); err != nil {
		return ""
	}
	return normalizePublicIP(response.IP, family)
}

func (d *NetworkIdentityDiscoverer) fetchCountryCode(ctx context.Context, client *http.Client, ip string) string {
	format := strings.TrimSpace(d.GeoIPURLFormat)
	if format == "" || strings.TrimSpace(ip) == "" {
		return ""
	}
	var response geoIPResponse
	if err := d.fetchJSON(ctx, client, fmt.Sprintf(format, ip), &response); err != nil {
		return ""
	}
	if !response.Success {
		return ""
	}
	return normalizeCountryCode(response.CountryCode)
}

func (d *NetworkIdentityDiscoverer) fetchJSON(ctx context.Context, client *http.Client, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Zeno-Agent")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("identity provider returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// CachedNetworkIdentityDiscoverer prevents Agent host reports from calling public
// identity providers every report interval. If a refresh totally fails, the last
// known identity is returned so transient provider/network failures do not clear
// previously discovered node metadata.
type CachedNetworkIdentityDiscoverer struct {
	Inner *NetworkIdentityDiscoverer
	TTL   time.Duration
	Now   func() time.Time

	mu        sync.Mutex
	cached    NetworkIdentity
	checkedAt time.Time
	hasCache  bool
}

func NewCachedNetworkIdentityDiscoverer(inner *NetworkIdentityDiscoverer, ttl time.Duration) *CachedNetworkIdentityDiscoverer {
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	return &CachedNetworkIdentityDiscoverer{Inner: inner, TTL: ttl, Now: time.Now}
}

func (d *CachedNetworkIdentityDiscoverer) Discover(ctx context.Context) NetworkIdentity {
	if d == nil {
		return NetworkIdentity{}
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	d.mu.Lock()
	if d.hasCache && now.Sub(d.checkedAt) < d.TTL {
		cached := d.cached
		d.mu.Unlock()
		return cached
	}
	d.mu.Unlock()

	identity := NetworkIdentity{}
	if d.Inner != nil {
		identity = d.Inner.Discover(ctx)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.checkedAt = now
	if identity.empty() && d.hasCache {
		return d.cached
	}
	if !identity.empty() {
		d.cached = identity
		d.hasCache = true
	}
	return identity
}

func normalizePublicIP(value string, family int) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	if family == 4 {
		ipv4 := ip.To4()
		if ipv4 == nil {
			return ""
		}
		return ipv4.String()
	}
	if family == 6 {
		if ip.To4() != nil || ip.To16() == nil {
			return ""
		}
		return ip.String()
	}
	return ""
}

func normalizeCountryCode(value string) string {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if len(trimmed) != 2 {
		return ""
	}
	for _, r := range trimmed {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return trimmed
}
