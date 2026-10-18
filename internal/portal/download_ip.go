package portal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Only the Worker and origin possess the signing key. Bind the address to the
// exact method, endpoint and download token, and accept it for at most 2 minutes.
// Unsigned requests keep the TCP peer; arbitrary forwarding headers are ignored.
func (s *Server) downloadVisitorIP(r *http.Request, secret string, now time.Time) (string, error) {
	ip := r.Header.Get("X-Download-Client-IP")
	stamp := r.Header.Get("X-Download-Timestamp")
	signature := r.Header.Get("X-Download-Signature")
	if ip == "" && stamp == "" && signature == "" {
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		return peer, nil
	}
	denied := errors.New("invalid download proxy proof")
	key, err := hex.DecodeString(s.cfg.DownloadProxySecret)
	if err != nil || len(key) != 32 || r.TLS == nil {
		return "", denied
	}
	address, err := netip.ParseAddr(ip)
	if err != nil || address.Zone() != "" || !address.IsGlobalUnicast() {
		return "", denied
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || len(stamp) > 11 || seconds < now.Unix()-120 || seconds > now.Unix()+120 {
		return "", denied
	}
	got, err := hex.DecodeString(signature)
	if err != nil || len(got) != sha256.Size {
		return "", denied
	}
	message := strings.Join([]string{"v1", stamp, r.Method, r.URL.Path, ip, downloadHash(secret)}, "\n")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return "", denied
	}
	return address.Unmap().String(), nil
}
