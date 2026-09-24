package network

import (
	"slices"
	"testing"
)

func TestParseNamespaceRoutesNormalizesEachFamily(t *testing.T) {
	ipv4, ipv6 := routeFamilies(100000)[0], routeFamilies(100000)[1]

	ipv4Output := "default via 10.6.26.137 dev vpeer0\n198.51.100.7 via 10.6.26.137 dev vpeer0\n"
	if got, want := parseNamespaceRoutes(ipv4Output, ipv4), []string{"0.0.0.0/0", "198.51.100.7/32"}; !slices.Equal(got, want) {
		t.Fatalf("IPv4 routes = %v, want %v", got, want)
	}

	// The mesh route belongs to the mesh registration, so the VM routes never remove it.
	ipv6Output := "default via fe80::1 dev vg-1 metric 1024 pref medium\n" +
		"fdaa::/16 via fe80::1 dev vg-1 metric 1024 pref medium\n" +
		"2000::/3 via fe80::1 dev vg-1 metric 1024 pref medium\n"
	if got, want := parseNamespaceRoutes(ipv6Output, ipv6), []string{"::/0", "2000::/3"}; !slices.Equal(got, want) {
		t.Fatalf("IPv6 routes = %v, want %v", got, want)
	}
}
