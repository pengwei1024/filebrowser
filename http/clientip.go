package fbhttp

import (
	"net"
	"net/http"
	"strings"
)

// unknownIP is used when the client address cannot be determined at all. It is
// never banned, because banning a placeholder would lock out every request
// whose address could not be resolved.
const unknownIP = "unknown"

// requestIP resolves the address to attribute a request to, preferring the real
// public client over the private hop that terminates the connection.
//
// Why not net/http's RemoteAddr alone: inside a container the connection comes
// from the Docker bridge, so RemoteAddr is always the gateway (e.g. 172.17.0.1)
// and every visitor looks identical. The real address only exists if a fronting
// proxy forwards it, so the headers are consulted first:
//
//  1. the left-most public address in X-Forwarded-For — proxies append as they
//     go, so the left-most entry is the originating client;
//  2. X-Real-IP, which most reverse proxies set to the immediate client;
//  3. RemoteAddr;
//  4. any valid private address seen along the way, so the result is still
//     stable per source rather than empty;
//  5. unknownIP.
//
// Trust note: X-Forwarded-For and X-Real-IP are client-controlled unless the
// fronting proxy overwrites them. With an IP-based ban in play, a proxy that
// merely appends to a client-supplied X-Forwarded-For lets an attacker rotate
// identities or get a third party banned. Configure the proxy to replace these
// headers.
func requestIP(r *http.Request) string {
	fallback := ""

	remember := func(ip string) {
		if fallback == "" {
			fallback = ip
		}
	}

	for _, part := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		ip := normalizeIP(part)
		if ip == "" {
			continue
		}
		if isPublicIP(ip) {
			return ip
		}
		remember(ip)
	}

	for _, header := range []string{"X-Real-Ip", "CF-Connecting-IP", "True-Client-IP"} {
		ip := normalizeIP(r.Header.Get(header))
		if ip == "" {
			continue
		}
		if isPublicIP(ip) {
			return ip
		}
		remember(ip)
	}

	if ip := normalizeIP(r.RemoteAddr); ip != "" {
		if isPublicIP(ip) {
			return ip
		}
		remember(ip)
	}

	if fallback != "" {
		return fallback
	}
	return unknownIP
}

// normalizeIP turns one raw header entry or RemoteAddr into a canonical IP
// string, tolerating surrounding whitespace, an attached port and bracketed
// IPv6 forms. It returns "" when the value is not an IP address.
func normalizeIP(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")

	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	return ip.String()
}

// cgnat is the carrier-grade NAT range (RFC 6598). Addresses in it identify a
// provider's NAT rather than a reachable client, so they are not treated as
// public.
var cgnat = net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// isPublicIP reports whether ip is a globally routable address, i.e. one that
// actually identifies an outside client.
func isPublicIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	switch {
	case parsed.IsLoopback(),
		parsed.IsPrivate(),
		parsed.IsUnspecified(),
		parsed.IsLinkLocalUnicast(),
		parsed.IsLinkLocalMulticast(),
		parsed.IsMulticast(),
		cgnat.Contains(parsed):
		return false
	}
	return true
}
