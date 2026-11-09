package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ramst/pause/internal/app"
)

func TestExplanationHTTP(t *testing.T) {
	f := newFixture(t)
	browserEngine(t, f)
	senior := f.newClient("127.0.0.141")
	senior.login(t, "senior1@example.test")
	created := senior.request("POST", "/api/intents", intake("900.00", false), map[string]string{"Idempotency-Key": uuid.NewString()})
	status(t, created, 201)
	i := readIntent(t, created)
	path := "/api/intents/" + i.ID + "/explanation?evidence_id="
	evidenceResponse := senior.request("GET", "/api/intents/"+i.ID+"/evidence", nil, nil)
	status(t, evidenceResponse, 200)
	var evidence app.EvidencePacket
	if err := json.Unmarshal(evidenceResponse.Body.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	path += evidence.ID
	for n, email := range []string{"senior1@example.test", "reviewer1@example.test", "subject1@example.test", "senior2@example.test", "reviewer2@example.test", ""} {
		c := f.newClient([]string{"127.0.0.142", "127.0.0.143", "127.0.0.144", "127.0.0.145", "127.0.0.146", "127.0.0.147"}[n])
		if email != "" {
			c.login(t, email)
		}
		response := c.request("GET", path, nil, nil)
		want := []int{200, 200, 403, 404, 404, 401}[n]
		status(t, response, want)
		if want == 200 {
			var explanation app.ExplanationDTO
			if err := json.Unmarshal(response.Body.Bytes(), &explanation); err != nil || explanation.Source != "AI" {
				t.Fatal(explanation, err)
			}
		}
	}
	status(t, senior.request("GET", "/api/intents/"+i.ID+"/explanation?evidence_id=bad", nil, nil), 404)
}
