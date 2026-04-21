package main

import (
	"fmt"
	"net"
	"net/url"
)

// privateCIDRs lists all private and reserved IP ranges that the HTTP API
// must not target. The CLI/MCP path (running on the user's own machine)
// is not subject to this restriction.
var privateCIDRs []*net.IPNet

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local
		"0.0.0.0/8",      // "this" network
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 unique local
		"fe80::/10",      // IPv6 link-local
	} {
		_, network, _ := net.ParseCIDR(cidr)
		privateCIDRs = append(privateCIDRs, network)
	}
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, cidr := range privateCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// validateBenchmarkURL ensures the target URL is safe for the server to
// request. It blocks private/reserved IP ranges and non-HTTP(S) schemes
// to prevent SSRF. This check is only applied to the HTTP API path;
// the CLI/MCP agent runs on the user's machine and may target localhost.
func validateBenchmarkURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http and https schemes are allowed")
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL must include a host")
	}

	// Resolve DNS to actual IPs to prevent DNS rebinding attacks
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}

	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf(
				"targeting private or reserved addresses is not allowed from the server — use the CLI agent for local endpoints",
			)
		}
	}

	return nil
}
