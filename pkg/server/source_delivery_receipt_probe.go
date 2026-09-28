package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

const (
	sourceDeliveryReceiptSchema     = "digitalogic.product-sync-receipt.v2"
	sourceDeliveryReceiptApplied    = "applied"
	sourceDeliveryReceiptPending    = "pending"
	sourceDeliveryReceiptSuperseded = "superseded"
	sourceDeliveryReceiptNotFound   = "not_found"
	sourceDeliveryReceiptMaxBytes   = 32 << 10
)

var errSourceDeliveryReceiptProbe = errors.New("source_delivery_receipt_probe_failed")

type sourceDeliveryReceiptProbe struct {
	Schema           string           `json:"schema"`
	Status           string           `json:"status"`
	EventID          string           `json:"event_id"`
	GeneratedAt      string           `json:"generated_at"`
	Source           canonical.Source `json:"source"`
	PendingProducts  int              `json:"pending_products"`
	DeferredProducts int              `json:"deferred_products"`
	ObservedAt       string           `json:"observed_at"`
}

type sourceDeliveryReceiptResponse struct {
	Success bool                       `json:"success"`
	Data    sourceDeliveryReceiptProbe `json:"data"`
}

type sourceDeliveryReceiptRequest struct {
	EventID     string           `json:"event_id"`
	GeneratedAt string           `json:"generated_at"`
	Source      canonical.Source `json:"source"`
}

func probeSourceDeliveryReceipt(ctx context.Context, cfg appconfig.Config, input *canonical.Envelope) (sourceDeliveryReceiptProbe, error) {
	if input == nil || strings.TrimSpace(input.EventID) == "" || strings.TrimSpace(input.GeneratedAt) == "" {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	if _, err := time.Parse(time.RFC3339Nano, input.GeneratedAt); err != nil {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	delivery := updateout.Normalize(cfg.SendUpdates)
	secret, err := updateout.ResolveProductSyncSecret(delivery)
	if err != nil || secret == "" {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	endpoint, err := sourceDeliveryReceiptProbeURL(delivery.URL)
	if err != nil {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	body, err := json.Marshal(sourceDeliveryReceiptRequest{EventID: input.EventID, GeneratedAt: input.GeneratedAt, Source: input.Source})
	if err != nil {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	timeout, err := time.ParseDuration(delivery.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 10 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "patris-export")
	request.Header.Set(updateout.ProductSyncSecretHeader, secret)
	response, err := sourceDeliveryReceiptHTTPClient().Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, sourceDeliveryReceiptMaxBytes+1))
	if err != nil || len(responseBody) > sourceDeliveryReceiptMaxBytes {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	var responseEnvelope sourceDeliveryReceiptResponse
	if decoder.Decode(&responseEnvelope) != nil || decoder.Decode(new(any)) != io.EOF || !responseEnvelope.Success {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	receipt := responseEnvelope.Data
	if validateSourceDeliveryReceiptProbe(receipt, input) != nil {
		return sourceDeliveryReceiptProbe{}, errSourceDeliveryReceiptProbe
	}
	return receipt, nil
}

func sourceDeliveryReceiptProbeURL(destination string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(destination))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", errSourceDeliveryReceiptProbe
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/receipt"
	return endpoint.String(), nil
}

func validateSourceDeliveryReceiptProbe(receipt sourceDeliveryReceiptProbe, input *canonical.Envelope) error {
	if receipt.Schema != sourceDeliveryReceiptSchema || receipt.EventID != input.EventID || receipt.GeneratedAt != input.GeneratedAt || !receipt.Source.SameIdentity(input.Source) {
		return errSourceDeliveryReceiptProbe
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.GeneratedAt); err != nil {
		return errSourceDeliveryReceiptProbe
	}
	switch receipt.Status {
	case sourceDeliveryReceiptApplied, sourceDeliveryReceiptPending, sourceDeliveryReceiptSuperseded, sourceDeliveryReceiptNotFound:
	default:
		return errSourceDeliveryReceiptProbe
	}
	if receipt.PendingProducts < 0 || receipt.DeferredProducts < 0 {
		return errSourceDeliveryReceiptProbe
	}
	if _, err := time.Parse(time.RFC3339, receipt.ObservedAt); err != nil {
		return errSourceDeliveryReceiptProbe
	}
	return nil
}

var sourceDeliveryReceiptHTTPClient = sync.OnceValue(func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
})
