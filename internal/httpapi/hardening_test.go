package httpapi_test

import (
	"strings"
	"testing"
)

func TestP06OperationalRouteAndDependencyAvailability(t *testing.T) {
	f := newFixture(t)
	senior := f.newClient("198.51.100.91")
	senior.login(t, "senior1@example.test")
	status(t, senior.request("GET", "/api/operations/status", nil, nil), 403)
	reviewer := f.newClient("198.51.100.92")
	reviewer.login(t, "reviewer1@example.test")
	status(t, reviewer.request("GET", "/api/operations/status", nil, nil), 200)
	status(t, reviewer.request("GET", "/release-readyz", nil, nil), 503)
	status(t, reviewer.request("GET", "/readyz", nil, nil), 200)
	r := reviewer.request("GET", "/api/operations/status", nil, nil)
	if strings.Contains(r.Body.String(), "example.test") || strings.Contains(r.Body.String(), "password") {
		t.Fatal("diagnostic data exposure")
	}
}
