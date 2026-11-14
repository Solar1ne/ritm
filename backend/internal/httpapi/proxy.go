package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var prefixes []netip.Prefix
	for _, item := range strings.Split(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil {
			return nil, errors.New("TRUSTED_PROXY_CIDRS must contain comma-separated IP CIDRs")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

// clientIP accepts exactly one IP from a configured, directly connected proxy.
// X-Forwarded-For chains and untrusted peers cannot influence authentication quotas.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || peer.Zone() != "" {
		return host
	}
	peer = peer.Unmap()
	for _, prefix := range s.trustedProxies {
		if !prefix.Contains(peer) {
			continue
		}
		values := r.Header.Values("X-Real-IP")
		if len(values) == 1 {
			client, err := netip.ParseAddr(strings.TrimSpace(values[0]))
			if err == nil && client.Zone() == "" && !client.IsUnspecified() && !client.IsMulticast() {
				return client.Unmap().String()
			}
		}
		break
	}
	return peer.String()
}
