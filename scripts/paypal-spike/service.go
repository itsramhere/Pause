package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

type spike struct {
	config         config
	store          *diskStore
	paypal         *paypalClient
	now            func() time.Time
	storageFailed  bool
	evidenceSource string
}

func (s *spike) save() error {
	if s.storageFailed {
		return errors.New("storage failed earlier; restart after investigating local evidence")
	}
	if err := s.store.save(); err != nil {
		s.storageFailed = true
		return err
	}
	return nil
}

func (s *spike) create(ctx context.Context, scenarioID string) (*run, error) {
	for _, sc := range scenarios {
		if sc.ID == scenarioID {
			r := newRun(s.config, sc, s.now().UTC())
			s.store.data.Runs[r.ID] = r
			if err := s.save(); err != nil {
				return r, err
			}
			return r, s.dispatch(ctx, r, "create", false)
		}
	}
	return nil, errors.New("unknown fixed scenario")
}

func (s *spike) action(ctx context.Context, r *run, kind, orderID string) error {
	if s.storageFailed {
		return errors.New("storage failure prevents provider mutations")
	}
	if r.MerchantID != s.config.MerchantID {
		return errors.New("configured merchant differs from the persisted run; restore the original merchant configuration")
	}
	if kind == "reconcile" {
		return s.reconcile(ctx, r)
	}
	if kind == "duplicate-create" {
		if r.State != "AWAITING_PAYER" || r.Pending != "" {
			return errors.New("duplicate probe requires a created, unapproved order")
		}
		return s.dispatch(ctx, r, "create", true)
	}
	if kind == "retry" {
		if r.Pending == "" {
			return errors.New("no unresolved operation to retry")
		}
		pending := r.Pending
		if r.OrderID != "" {
			if err := s.reconcile(ctx, r); err != nil {
				return err
			}
			if r.Pending == "" {
				return nil
			}
		}
		return s.dispatch(ctx, r, pending, true)
	}
	if r.Pending != "" {
		return errors.New("unresolved operation blocks competing mutations; reconcile first")
	}
	switch kind {
	case "authorize":
		if r.State != "AWAITING_PAYER" || orderID == "" || r.OrderID != orderID {
			return errors.New("approval must match this persisted order")
		}
		o, _, err := s.paypal.getOrder(ctx, r.OrderID)
		if err != nil {
			return err
		}
		if err = validateOrder(r, o); err != nil {
			return err
		}
		if o.Status != "APPROVED" || len(o.Units[0].Payments.Authorizations) != 0 || len(o.Units[0].Payments.Captures) != 0 {
			return errors.New("order must be approved and have no existing payments before authorization")
		}
	case "capture":
		if r.Scenario.Terminal != "capture" || r.State != "HELD" || !r.AuthorizedWithoutCapture || !s.now().Before(r.Deadline) {
			return errors.New("capture requires the positive scenario, a confirmed uncaptured hold, and an unexpired application deadline")
		}
		if err := s.reconcile(ctx, r); err != nil {
			return err
		}
		if r.State != "HELD" {
			return errors.New("provider no longer confirms an uncaptured authorization")
		}
	case "void":
		if r.State != "HELD" || (r.Scenario.Terminal != "void" && s.now().Before(r.Deadline)) {
			return errors.New("void requires the denied scenario or an elapsed deadline, with an uncaptured hold")
		}
		if err := s.reconcile(ctx, r); err != nil {
			return err
		}
		if r.State != "HELD" {
			return errors.New("provider no longer confirms an uncaptured authorization")
		}
	default:
		return errors.New("unknown action")
	}
	return s.dispatch(ctx, r, kind, false)
}

func (s *spike) dispatch(ctx context.Context, r *run, kind string, replay bool) error {
	if s.storageFailed {
		return errors.New("storage failure prevents provider mutations")
	}
	if kind == "capture" && !s.now().Before(r.Deadline) {
		return errors.New("application deadline elapsed before dispatch; reconcile an existing capture, otherwise request void")
	}
	op := r.Operations[kind]
	if op == nil {
		if replay {
			return errors.New("persisted operation missing")
		}
		var body []byte
		switch kind {
		case "create":
			body = createBody(r)
		case "authorize":
			body = []byte(`{}`)
		case "capture":
			body = captureBody(r)
		case "void":
			body = nil
		default:
			return errors.New("unknown operation")
		}
		op = &operation{Kind: kind, RequestID: newID(), Body: string(body), RequestHash: hash(body), StartedAt: s.now().UTC(), Outcome: "RESERVED"}
		r.Operations[kind] = op
	} else {
		if !replay {
			return errors.New("operation already exists; reconcile or reuse its persisted key")
		}
		// This is a local operator timebox, NOT a provider retention guarantee.
		// No automatic POST retries; later retries need provider-specific investigation.
		if s.now().Sub(op.StartedAt) > 10*time.Minute {
			return errors.New("local retry timebox elapsed; retention is unproven, reconcile and investigate instead")
		}
		if op.RequestHash != hash([]byte(op.Body)) {
			return errors.New("persisted operation payload digest mismatch")
		}
	}
	duplicate := kind == "create" && r.OrderID != ""
	r.Pending = kind
	r.State = "RECONCILING"
	r.LastError = ""
	op.Attempts++
	op.Outcome = "DISPATCHED"
	// Durable before dispatch. On crash, Pending survives; a new competing key is never generated.
	if err := s.save(); err != nil {
		return err
	}
	path := "/v2/checkout/orders"
	switch kind {
	case "authorize":
		path += "/" + url.PathEscape(r.OrderID) + "/authorize"
	case "capture", "void":
		path = "/v2/payments/authorizations/" + url.PathEscape(r.AuthorizationID) + "/" + kind
	}
	resp, err := s.paypal.call(ctx, "POST", path, op.RequestID, []byte(op.Body))
	op.HTTPStatus = resp.Status
	op.DebugID = resp.DebugID
	if err == nil {
		switch kind {
		case "create":
			var o order
			if (resp.Status != 200 && resp.Status != 201) || json.Unmarshal(resp.Body, &o) != nil {
				err = errors.New("unexpected create result; reconcile before continuing")
			} else if err = validateOrder(r, o); err == nil {
				if o.Status != "CREATED" && o.Status != "PAYER_ACTION_REQUIRED" {
					err = errors.New("created order has an unexpected provider status")
				} else if len(o.Units[0].Payments.Captures) != 0 || len(o.Units[0].Payments.Authorizations) != 0 {
					err = errors.New("created order unexpectedly contains payments")
				} else {
					r.OrderID = o.ID
					r.OrderStatus = o.Status
					r.Pending = ""
					r.State = "AWAITING_PAYER"
					if duplicate {
						r.DuplicateCreateSameID = true
					}
				}
			}
		case "authorize":
			if resp.Status != 200 && resp.Status != 201 {
				err = errors.New("authorization not synchronously confirmed")
			} else {
				err = s.reconcileProvider(ctx, r)
			}
		case "capture":
			var p payment
			if (resp.Status != 200 && resp.Status != 201) || json.Unmarshal(resp.Body, &p) != nil {
				err = errors.New("unexpected capture result; outcome unknown")
			} else if err = validatePayment(r, p); err == nil {
				if p.Supplementary.Related.AuthorizationID != "" && p.Supplementary.Related.AuthorizationID != r.AuthorizationID {
					err = errors.New("capture authorization binding mismatch")
				} else {
					r.CaptureID = p.ID
					r.CaptureStatus = p.Status
					err = s.reconcileProvider(ctx, r)
				}
			}
		case "void":
			if resp.Status != 204 && resp.Status != 200 {
				err = errors.New("unexpected void response")
			} else {
				err = s.reconcileProvider(ctx, r)
			}
		}
	}
	if err != nil {
		r.State = "RECONCILING"
		r.LastError = err.Error()
		op.Outcome = "UNRESOLVED"
	} else if r.Pending == "" {
		op.Outcome = "CONFIRMED"
	} else {
		op.Outcome = "UNRESOLVED"
	}
	if e := s.save(); e != nil {
		return e
	}
	if e := s.paymentReceipt(r, op); e != nil {
		return e
	}
	return err
}

func (s *spike) paymentReceipt(r *run, operation any) error {
	// Whitelist-only receipt. No raw response, OAuth tokens, payer data, headers, or cookies.
	receipt := map[string]any{"source": s.evidenceSource, "recorded_at": s.now().UTC(), "run_id": r.ID, "scenario_id": r.Scenario.ID, "operation": operation, "order_id": r.OrderID, "authorization_id": r.AuthorizationID, "capture_id": r.CaptureID, "actual_merchant_id": r.MerchantID, "claimed_subject": r.Scenario.Subject, "amount_minor": r.Scenario.AmountMinor, "currency": r.Currency, "state": r.State, "authorization_status": r.AuthorizationStatus, "capture_status": r.CaptureStatus, "authorized_without_capture": r.AuthorizedWithoutCapture, "duplicate_create_same_id": r.DuplicateCreateSameID, "error": r.LastError}
	if err := s.store.receipt(receipt); err != nil {
		s.storageFailed = true
		return err
	}
	return nil
}

func (s *spike) reconcile(ctx context.Context, r *run) error {
	err := s.reconcileProvider(ctx, r)
	if err != nil {
		r.LastError = err.Error()
		r.State = "RECONCILING"
	} else {
		r.LastError = ""
	}
	if e := s.save(); e != nil {
		return e
	}
	if e := s.paymentReceipt(r, map[string]any{"kind": "reconcile", "mutating": false, "note": "Order and available authorization/capture read results; no payment mutation dispatched."}); e != nil {
		return e
	}
	return err
}

func (s *spike) reconcileProvider(ctx context.Context, r *run) error {
	if r.OrderID == "" {
		return errors.New("no order ID was received; only a same-key create retry can recover this spike")
	}
	o, _, err := s.paypal.getOrder(ctx, r.OrderID)
	if err != nil {
		return err
	}
	if err = validateOrder(r, o); err != nil {
		return err
	}
	r.OrderStatus = o.Status
	u := o.Units[0]
	if len(u.Payments.Authorizations) == 0 {
		if len(u.Payments.Captures) > 0 {
			return errors.New("unexpected capture without authorization; investigate")
		}
		if r.AuthorizationID != "" {
			return errors.New("provider order lost its authorization mapping")
		}
		if r.Pending == "create" {
			r.Pending = ""
		}
		if r.Pending != "" {
			r.State = "RECONCILING"
		} else {
			r.State = "AWAITING_PAYER"
		}
		return nil
	}
	if len(u.Payments.Authorizations) != 1 {
		return errors.New("expected exactly one authorization")
	}
	a := u.Payments.Authorizations[0]
	if err = validatePayment(r, a); err != nil {
		return err
	}
	if r.AuthorizationID != "" && r.AuthorizationID != a.ID {
		return errors.New("authorization ID changed unexpectedly")
	}
	r.AuthorizationID = a.ID
	a, _, err = s.paypal.getAuthorization(ctx, a.ID)
	if err != nil {
		return err
	}
	if err = validatePayment(r, a); err != nil {
		return err
	}
	if a.ID != r.AuthorizationID {
		return errors.New("authorization read returned a different ID")
	}
	r.AuthorizationStatus = a.Status
	var completed *payment
	for _, p := range u.Payments.Captures {
		if err = validatePayment(r, p); err != nil {
			return err
		}
		if p.Status == "COMPLETED" {
			if completed != nil {
				return errors.New("multiple completed captures; investigate")
			}
			copy := p
			completed = &copy
		}
	}
	if r.CaptureID != "" {
		resp, e := s.paypal.call(ctx, "GET", "/v2/payments/captures/"+url.PathEscape(r.CaptureID), "", nil)
		if e != nil {
			return e
		}
		var p payment
		if resp.Status != 200 || json.Unmarshal(resp.Body, &p) != nil {
			return errors.New("capture read invalid")
		}
		if err = validatePayment(r, p); err != nil {
			return err
		}
		if p.ID != r.CaptureID {
			return errors.New("capture read ID mismatch")
		}
		r.CaptureStatus = p.Status
		if p.Status == "COMPLETED" {
			completed = &p
		}
	}
	if completed != nil {
		if a.Status != "CAPTURED" {
			return errors.New("capture and authorization disagree; investigate")
		}
		r.CaptureID = completed.ID
		r.CaptureStatus = completed.Status
		r.State = "CAPTURED"
		settle(r, "capture")
		return nil
	}
	switch a.Status {
	case "VOIDED":
		if r.CaptureID != "" || len(u.Payments.Captures) > 0 {
			return errors.New("void result conflicts with capture evidence")
		}
		r.State = "VOIDED"
		settle(r, "void")
	case "CREATED":
		if len(u.Payments.Captures) > 0 || r.CaptureID != "" {
			r.State = "RECONCILING"
			return nil
		}
		if r.Pending == "authorize" {
			r.AuthorizedWithoutCapture = true
			settle(r, "authorize")
		}
		if r.Pending != "" {
			r.State = "RECONCILING"
		} else {
			r.State = "HELD"
		}
	case "DENIED", "EXPIRED":
		r.State = "CLOSED_UNCAPTURED"
		if r.Pending == "authorize" {
			settle(r, "authorize")
		}
	default:
		r.State = "RECONCILING"
	}
	return nil
}

func settle(r *run, kind string) {
	if r.Pending == kind {
		r.Pending = ""
	}
	if op := r.Operations[kind]; op != nil {
		op.Outcome = "CONFIRMED"
	}
}
