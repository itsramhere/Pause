package httpapi

import (
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/config"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestP06TrustedProxyChain(t *testing.T) {
	s := &Server{App: &app.Service{Config: config.Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}}}}
	for _, c := range []struct{ peer, chain, want string }{
		{"198.51.100.1:4000", "1.1.1.1", "198.51.100.1"},
		{"10.42.0.3:4000", "1.1.1.1, 198.51.100.7", "198.51.100.7"},
		{"10.42.0.3:4000", "198.51.100.7, 10.42.0.2", "198.51.100.7"},
		{"10.42.0.3:4000", "garbage", "10.42.0.3"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		r.Header.Set("X-Forwarded-For", c.chain)
		if got := s.peer(r); got != c.want {
			t.Fatalf("got %s want %s", got, c.want)
		}
	}
}
