// Package pricingcurrency transports currency intent to its WordPress owner.
// It does not calculate prices, retry submissions, or resolve credentials.
package pricingcurrency

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

var requestPattern = regexp.MustCompile(`^[a-zA-Z0-9._:-]{8,128}$`)
var revisionPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var jobPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var statusPattern = regexp.MustCompile(`^[a-z_]{1,48}$`)
var pricePattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

type Client struct {
	Origin, Key, Secret string
	HTTPClient          *http.Client
}

// Error contains only bounded local diagnostics, never remote text or secrets.
// OutcomeUnknown means a POST may have reached its owner; observe the same ID.
type Error struct {
	Code           string
	OutcomeUnknown bool
	StatusCode     int
	// NotFound is true only for an exact owner request-not-found response.
	NotFound bool
}

func (e *Error) Error() string { return "currency owner: " + e.Code }

type Job struct {
	JobID           string                     `json:"job_id"`
	Generation      int64                      `json:"generation"`
	RequestID       string                     `json:"request_id"`
	Status          string                     `json:"status"`
	DesiredCurrency map[string]json.RawMessage `json:"desired_currency"`
	StateRevision   string                     `json:"state_revision,omitempty"`
}

type State struct {
	StateRevision    string          `json:"state_revision"`
	YuanPrice        json.RawMessage `json:"yuan_price"`
	DollarPrice      json.RawMessage `json:"dollar_price"`
	CNYEffectiveDate json.RawMessage `json:"cny_effective_date"`
	USDEffectiveDate json.RawMessage `json:"usd_effective_date"`
}

func (s State) Currency() map[string]json.RawMessage {
	return map[string]json.RawMessage{"yuan_price": s.YuanPrice, "dollar_price": s.DollarPrice, "cny_effective_date": s.CNYEffectiveDate, "usd_effective_date": s.USDEffectiveDate}
}

func (c *Client) Submit(ctx context.Context, requestID, expectedRevision string, values map[string]string) (Job, error) {
	if !requestPattern.MatchString(requestID) || !revisionPattern.MatchString(expectedRevision) || len(values) == 0 {
		return Job{}, &Error{Code: "invalid_request"}
	}
	body := map[string]string{"request_id": requestID, "expected_state_revision": expectedRevision}
	for k, v := range values {
		switch k {
		case "yuan_price", "dollar_price":
			if !pricePattern.MatchString(v) {
				return Job{}, &Error{Code: "invalid_currency_value"}
			}
		case "cny_effective_date", "usd_effective_date":
			d, e := time.Parse("2006-01-02", v)
			if e != nil || d.Format("2006-01-02") != v {
				return Job{}, &Error{Code: "invalid_currency_value"}
			}
		default:
			return Job{}, &Error{Code: "invalid_currency_field"}
		}
		body[k] = v
	}
	var job Job
	err := c.call(ctx, "POST", "currency", body, requestID, expectedRevision, &job)
	if err == nil {
		err = validateJob(job, requestID, true)
	}
	return job, err
}

func (c *Client) Observe(ctx context.Context, requestID string) (Job, error) {
	if !requestPattern.MatchString(requestID) {
		return Job{}, &Error{Code: "invalid_request"}
	}
	var job Job
	err := c.call(ctx, "GET", "currency/requests/"+url.PathEscape(requestID), nil, "", "", &job)
	if err == nil {
		err = validateJob(job, requestID, false)
	}
	return job, err
}

func validateJob(j Job, id string, write bool) error {
	if !jobPattern.MatchString(j.JobID) || j.Generation < 1 || j.RequestID != id || !statusPattern.MatchString(j.Status) {
		return &Error{Code: "job_identity_unverified", OutcomeUnknown: write}
	}
	return nil
}

func (c *Client) Read(ctx context.Context) (State, error) {
	var state State
	err := c.call(ctx, "GET", "currency", nil, "", "", &state)
	if err == nil && !revisionPattern.MatchString(state.StateRevision) {
		err = &Error{Code: "owner_state_unverified"}
	}
	return state, err
}

func (c *Client) call(ctx context.Context, method, path string, body any, id, revision string, out any) error {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.Contains(c.Origin, "#") || c.Key == "" || c.Secret == "" || strings.ContainsAny(c.Key+c.Secret, "\r\n") || strings.Contains(c.Key, ":") {
		return &Error{Code: "invalid_configuration"}
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return &Error{Code: "invalid_request"}
		}
	}
	endpoint := u.Scheme + "://" + u.Host + "/wp-json/digitalogic/v1/" + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return &Error{Code: "invalid_request"}
	}
	req.SetBasicAuth(c.Key, c.Secret)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", `"`+revision+`"`)
		req.Header.Set("Idempotency-Key", id)
	}
	client := http.Client{Timeout: 30 * time.Second}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 {
		client.Timeout = 30 * time.Second
	}
	write := method == "POST"
	if ctx.Err() != nil {
		return &Error{Code: "request_cancelled"}
	}
	response, err := client.Do(req)
	if err != nil {
		return &Error{Code: "transport_failed", OutcomeUnknown: write}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return &Error{Code: "response_unverified", OutcomeUnknown: write, StatusCode: response.StatusCode}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var rejection struct {
			Success *bool  `json:"success"`
			Code    string `json:"code"`
		}
		notFound := method == "GET" && strings.HasPrefix(path, "currency/requests/") && response.StatusCode == http.StatusNotFound && json.Unmarshal(data, &rejection) == nil && rejection.Success != nil && !*rejection.Success && rejection.Code == "digitalogic_currency_async_request_not_found"
		return &Error{Code: "owner_rejected", OutcomeUnknown: write, StatusCode: response.StatusCode, NotFound: notFound}
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Success == nil || !*envelope.Success || len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, out) != nil {
		return &Error{Code: "response_unverified", OutcomeUnknown: write, StatusCode: response.StatusCode}
	}
	return nil
}
