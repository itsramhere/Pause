package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ramst/pause/internal/app"
)

func TestRequestStoryHTTP(t *testing.T) {
	f := newFixture(t)
	f.svc.Investigation = nil
	senior := f.newClient("127.0.0.131")
	senior.login(t, "senior1@example.test")
	out := senior.request("POST", "/api/intents", intake("900.00", true), map[string]string{"Idempotency-Key": uuid.NewString()})
	status(t, out, 201)
	i := readIntent(t, out)
	path := "/api/intents/" + i.ID + "/history"
	for n, email := range []string{"senior1@example.test", "reviewer1@example.test", "subject1@example.test", "senior2@example.test", "reviewer2@example.test", ""} {
		c := f.newClient("127.0.0." + []string{"132", "133", "134", "135", "136", "137"}[n])
		if email != "" {
			c.login(t, email)
		}
		out := c.request("GET", path, nil, nil)
		want := []int{200, 200, 403, 404, 404, 401}[n]
		status(t, out, want)
		if want == 200 {
			var story app.RequestStory
			if err := json.Unmarshal(out.Body.Bytes(), &story); err != nil {
				t.Fatal(err)
			}
			if !story.HistoryEnabled || len(story.Rows) != 1 || story.Rows[0].IntentID != i.ID {
				t.Fatal(story)
			}
		}
	}
}
