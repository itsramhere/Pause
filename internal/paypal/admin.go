package paypal

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

type EventSummary struct {
	ID       string `json:"id"`
	Type     string `json:"event_type"`
	Created  string `json:"create_time"`
	Resource struct {
		ID string `json:"id"`
	} `json:"resource"`
}

func (c *Client) EventTypes(ctx context.Context) ([]string, error) {
	b, _, e := c.call(ctx, "GET", "/v1/notifications/webhooks-event-types", "", nil)
	if e != nil {
		return nil, e
	}
	var v struct {
		Types []struct {
			Name string `json:"name"`
		} `json:"event_types"`
	}
	if json.Unmarshal(b, &v) != nil {
		return nil, &Error{Code: "PROVIDER_INVALID_JSON"}
	}
	names := []string{}
	for _, t := range v.Types {
		names = append(names, t.Name)
	}
	return names, nil
}
func (c *Client) Events(ctx context.Context, start, end time.Time) ([]EventSummary, error) {
	q := url.Values{"start_time": {start.UTC().Format(time.RFC3339)}, "end_time": {end.UTC().Format(time.RFC3339)}, "page_size": {"100"}}
	b, _, e := c.call(ctx, "GET", "/v1/notifications/webhooks-events?"+q.Encode(), "", nil)
	if e != nil {
		return nil, e
	}
	var v struct {
		Events []EventSummary `json:"events"`
	}
	if json.Unmarshal(b, &v) != nil {
		return nil, &Error{Code: "PROVIDER_INVALID_JSON"}
	}
	return v.Events, nil
}
func (c *Client) RegisterWebhook(ctx context.Context, address string, types []string) (string, error) {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/webhooks/paypal" {
		return "", &Error{Code: "WEBHOOK_URL_INVALID"}
	}
	available, e := c.EventTypes(ctx)
	if e != nil {
		return "", e
	}
	set := map[string]bool{}
	for _, v := range available {
		set[v] = true
	}
	entries := []map[string]string{}
	for _, name := range types {
		if !set[name] {
			return "", &Error{Code: "WEBHOOK_EVENT_UNSUPPORTED"}
		}
		entries = append(entries, map[string]string{"name": name})
	}
	body, _ := json.Marshal(map[string]any{"url": address, "event_types": entries})
	b, _, e := c.call(ctx, "POST", "/v1/notifications/webhooks", "", body)
	if e != nil {
		return "", e
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &result) != nil || safe(result.ID) == "" {
		return "", &Error{Code: "PROVIDER_INVALID_JSON"}
	}
	return result.ID, nil
}
func (c *Client) ResendEvent(ctx context.Context, event, webhook string) error {
	if safe(event) == "" || safe(webhook) == "" {
		return &Error{Code: "WEBHOOK_ID_INVALID"}
	}
	body, _ := json.Marshal(map[string]any{"webhook_ids": []string{webhook}})
	_, _, e := c.call(ctx, "POST", "/v1/notifications/webhooks-events/"+url.PathEscape(event)+"/resend", "", body)
	return e
}
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	if safe(id) == "" {
		return &Error{Code: "WEBHOOK_ID_INVALID"}
	}
	_, _, e := c.call(ctx, "DELETE", "/v1/notifications/webhooks/"+url.PathEscape(id), "", nil)
	return e
}
