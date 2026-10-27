// webhook-relay exposes only the signed provider ingestion path to a temporary
// HTTPS tunnel. It never forwards sessions, general APIs, or diagnostics.
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

func main() {
	upstream := os.Getenv("WEBHOOK_UPSTREAM")
	if upstream == "" {
		upstream = "http://127.0.0.1:8080"
	}
	u, e := url.Parse(upstream)
	if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		log.Fatal("loopback HTTP upstream required")
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ErrorLog = log.New(os.Stderr, "webhook upstream error: ", 0)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/webhooks/paypal" || r.Method != "POST" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		r.Header.Del("X-CSRF-Token")
		proxy.ServeHTTP(w, r)
	})
	s := http.Server{Addr: "127.0.0.1:8090", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 45 * time.Second, MaxHeaderBytes: 16 * 1024}
	log.Fatal(s.ListenAndServe())
}
