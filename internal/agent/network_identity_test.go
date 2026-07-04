package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNetworkIdentityDiscovererReturnsPartialIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4":
			w.Write([]byte(`{"ip":"203.0.113.10"}`))
		case "/v6":
			w.Write([]byte(`{"ip":"2001:db8::10"}`))
		case "/geo/203.0.113.10":
			w.Write([]byte(`{"success":true,"country_code":"jp","ip":"203.0.113.10"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	discoverer := &NetworkIdentityDiscoverer{
		Client:         server.Client(),
		IPv4URL:        server.URL + "/v4",
		IPv6URL:        server.URL + "/v6",
		GeoIPURLFormat: server.URL + "/geo/%s",
	}

	identity := discoverer.Discover(context.Background())
	if identity.PublicIPv4 != "203.0.113.10" || identity.PublicIPv6 != "2001:db8::10" || identity.CountryCode != "JP" {
		t.Fatalf("identity = %+v, want normalized IPv4/IPv6/country", identity)
	}
}

func TestNetworkIdentityDiscovererIgnoresInvalidProviderValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4":
			w.Write([]byte(`{"ip":"2001:db8::10"}`))
		case "/v6":
			w.Write([]byte(`{"ip":"203.0.113.10"}`))
		case "/geo/203.0.113.10", "/geo/2001:db8::10":
			w.Write([]byte(`{"success":true,"country_code":"too-long"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	discoverer := &NetworkIdentityDiscoverer{
		Client:         server.Client(),
		IPv4URL:        server.URL + "/v4",
		IPv6URL:        server.URL + "/v6",
		GeoIPURLFormat: server.URL + "/geo/%s",
	}

	identity := discoverer.Discover(context.Background())
	if identity.PublicIPv4 != "" || identity.PublicIPv6 != "" || identity.CountryCode != "" {
		t.Fatalf("identity = %+v, want invalid provider values ignored", identity)
	}
}

func TestCachedNetworkIdentityDiscovererKeepsLastIdentityOnFullFailure(t *testing.T) {
	failProviders := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failProviders {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v4":
			w.Write([]byte(`{"ip":"198.51.100.8"}`))
		case "/v6":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/geo/198.51.100.8":
			w.Write([]byte(`{"success":true,"country_code":"HK"}`))
			failProviders = true
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	current := time.Unix(100, 0)
	cached := NewCachedNetworkIdentityDiscoverer(&NetworkIdentityDiscoverer{
		Client:         server.Client(),
		IPv4URL:        server.URL + "/v4",
		IPv6URL:        server.URL + "/v6",
		GeoIPURLFormat: server.URL + "/geo/%s",
	}, time.Minute)
	cached.Now = func() time.Time { return current }

	first := cached.Discover(context.Background())
	if first.PublicIPv4 != "198.51.100.8" || first.CountryCode != "HK" {
		t.Fatalf("first identity = %+v, want populated cache", first)
	}
	current = current.Add(2 * time.Minute)
	second := cached.Discover(context.Background())
	if second != first {
		t.Fatalf("second identity = %+v, want cached identity %+v after provider failure", second, first)
	}
}
