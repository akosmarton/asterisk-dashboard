package web

import (
	"net"
	"net/http"
	"strings"
)

// clientIP extracts the browser's address from X-Forwarded-For, but only when
// the direct peer is trusted. Trust follows Echo's defaults: loopback,
// link-local unicast and private addresses (RFC 1918 and RFC 4193 ULA). The
// chain is walked from the nearest hop outwards; the first untrusted address is
// the client. Without a trusted peer, the transport address is the answer.
func clientIP(req *http.Request) string {
	direct := remoteIP(req.RemoteAddr)
	if direct == nil {
		return ""
	}
	if !trustedIP(direct) {
		return direct.String()
	}

	if values := req.Header.Values("X-Forwarded-For"); len(values) > 0 {
		parts := strings.Split(strings.Join(values, ","), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := parseIP(parts[i])
			if ip == nil {
				// A malformed chain is not safe to interpret; fall back to the
				// transport peer rather than trusting an arbitrary hop.
				return direct.String()
			}
			if !trustedIP(ip) {
				return ip.String()
			}
		}
		// Every hop is trusted. Match Echo's ExtractIPFromXFFHeader and return
		// the furthest address; the edge proxy must have sanitized the header.
		if ip := parseIP(parts[0]); ip != nil {
			return ip.String()
		}
	}
	return direct.String()
}

func trustedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate()
}

func parseIP(value string) net.IP {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if ip := net.ParseIP(value); ip != nil {
		return ip
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return net.ParseIP(host)
	}
	return nil
}

func remoteIP(addr string) net.IP {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(addr)
}
