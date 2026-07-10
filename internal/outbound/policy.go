// Package outbound centralizes validation and connection policy for remote HTTP requests.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

var (
	// ErrURLNotAllowed indicates that an outbound URL violates the HTTP destination policy.
	ErrURLNotAllowed = errors.New("outbound URL is not allowed")
	// ErrDestinationNotPublic indicates that a destination resolved to a non-public address.
	ErrDestinationNotPublic = errors.New("outbound destination is not public")
)

// Resolver resolves a hostname to all of its current addresses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// LookupIPAddrFunc adapts a function to Resolver.
type LookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)

// LookupNetIP resolves a hostname through the wrapped legacy address function.
func (f LookupIPAddrFunc) LookupNetIP(ctx context.Context, _, host string) ([]netip.Addr, error) {
	resolved, err := f(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("lookup IP addresses: %w", err)
	}

	addresses := make([]netip.Addr, 0, len(resolved))
	for _, resolvedAddr := range resolved {
		addr, ok := netip.AddrFromSlice(resolvedAddr.IP)
		if !ok || resolvedAddr.Zone != "" {
			return nil, ErrDestinationNotPublic
		}

		addresses = append(addresses, addr.Unmap())
	}

	return addresses, nil
}

type destination struct {
	scheme    string
	host      string
	port      string
	addresses []netip.Addr
}

type authority struct {
	scheme string
	host   string
}

//nolint:gochecknoglobals // The centralized special-use range policy is immutable after initialization.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// ValidateURL checks the syntax, scheme, authority, port, and any literal address of an outbound URL.
func ValidateURL(target *url.URL) error {
	_, err := destinationForURL(target)

	return err
}

// ValidateResolvedURL checks URL policy and requires every resolved address to be public.
func ValidateResolvedURL(ctx context.Context, target *url.URL, resolver Resolver) error {
	_, err := resolveDestination(ctx, target, resolver)

	return err
}

func destinationForURL(target *url.URL) (destination, error) {
	validated, err := validatedAuthority(target)
	if err != nil {
		return destination{}, err
	}

	host, err := canonicalHost(validated.host)
	if err != nil {
		return destination{}, err
	}

	var result destination

	result.scheme = validated.scheme
	result.host = host
	result.port = effectivePort(validated.scheme)

	result.addresses, err = approvedLiteralAddresses(host)
	if err != nil {
		return destination{}, err
	}

	return result, nil
}

func validatedAuthority(target *url.URL) (authority, error) {
	if target == nil || target.Opaque != "" || target.User != nil {
		return authority{}, ErrURLNotAllowed
	}

	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return authority{}, ErrURLNotAllowed
	}

	rawHost := target.Hostname()

	port := target.Port()
	if !isUnambiguousAuthority(target.Host, rawHost, port) || !isAllowedPort(scheme, port) {
		return authority{}, ErrURLNotAllowed
	}

	return authority{scheme: scheme, host: rawHost}, nil
}

func resolveDestination(ctx context.Context, target *url.URL, resolver Resolver) (destination, error) {
	result, err := destinationForURL(target)
	if err != nil {
		return destination{}, err
	}

	if len(result.addresses) > 0 {
		return result, nil
	}

	if resolver == nil {
		return destination{}, fmt.Errorf("%w: resolver unavailable", ErrDestinationNotPublic)
	}

	resolved, err := resolver.LookupNetIP(ctx, "ip", result.host)
	if err != nil {
		return destination{}, fmt.Errorf("resolve outbound host: %w", err)
	}

	result.addresses, err = validatedAddresses(resolved)
	if err != nil {
		return destination{}, err
	}

	return result, nil
}

func canonicalHost(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed != raw || strings.Contains(trimmed, "%") || strings.HasSuffix(trimmed, "..") {
		return "", ErrURLNotAllowed
	}

	trimmed = strings.TrimSuffix(trimmed, ".")

	addr, err := netip.ParseAddr(trimmed)
	if err == nil {
		return canonicalIPHost(addr)
	}

	return canonicalDNSHost(trimmed)
}

func approvedLiteralAddresses(host string) ([]netip.Addr, error) {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		//nolint:nilerr // A parse failure means this is a DNS name resolved before dialing.
		return nil, nil
	}

	addr = addr.Unmap()
	if !isPublicAddress(addr) {
		return nil, ErrDestinationNotPublic
	}

	return []netip.Addr{addr}, nil
}

func canonicalIPHost(addr netip.Addr) (string, error) {
	if addr.Zone() != "" || addr.Is4In6() {
		return "", ErrURLNotAllowed
	}

	return addr.String(), nil
}

func canonicalDNSHost(host string) (string, error) {
	if strings.Contains(host, ":") {
		return "", ErrURLNotAllowed
	}

	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || !isValidDNSName(ascii) {
		return "", ErrURLNotAllowed
	}

	return strings.ToLower(ascii), nil
}

func isUnambiguousAuthority(authority, host, port string) bool {
	if authority == "" || host == "" {
		return false
	}

	expected := host
	if strings.Contains(host, ":") {
		expected = "[" + host + "]"
	}

	if port != "" {
		expected += ":" + port
	}

	return strings.EqualFold(authority, expected)
}

func isAllowedPort(scheme, port string) bool {
	if port == "" {
		return true
	}

	parsed, err := strconv.Atoi(port)
	if err != nil || strconv.Itoa(parsed) != port {
		return false
	}

	return port == effectivePort(scheme)
}

func effectivePort(scheme string) string {
	if scheme == "http" {
		return "80"
	}

	return "443"
}

func isValidDNSName(host string) bool {
	if host == "" || len(host) > 253 || host == "localhost" {
		return false
	}

	for label := range strings.SplitSeq(host, ".") {
		if !isValidDNSLabel(label) {
			return false
		}
	}

	return true
}

func isValidDNSLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}

	for _, char := range label {
		if !isDNSLabelChar(char) {
			return false
		}
	}

	return true
}

func isDNSLabelChar(char rune) bool {
	return char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' ||
		char == '-'
}

func validatedAddresses(resolved []netip.Addr) ([]netip.Addr, error) {
	if len(resolved) == 0 {
		return nil, ErrDestinationNotPublic
	}

	addresses := make([]netip.Addr, 0, len(resolved))
	for _, addr := range resolved {
		if !addr.IsValid() || addr.Zone() != "" || addr.Is4In6() {
			return nil, ErrDestinationNotPublic
		}

		if !isPublicAddress(addr) {
			return nil, ErrDestinationNotPublic
		}

		addresses = append(addresses, addr)
	}

	return addresses, nil
}

func isPublicAddress(addr netip.Addr) bool {
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return false
	}

	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}

	return addr != netip.MustParseAddr("168.63.129.16")
}
