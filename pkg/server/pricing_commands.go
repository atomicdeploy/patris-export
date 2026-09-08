package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/pricingtransport"
	"github.com/atomicdeploy/patris-export/pkg/updateout"
)

var errPricingCommandsUnavailable = &pricingtransport.Error{Code: "command_transport_unavailable"}

type pricingCommandConnection interface {
	Catalog(context.Context) (json.RawMessage, error)
	Receive(context.Context, []byte) (json.RawMessage, error)
	Close() error
}

type pricingCommandState struct {
	mu      sync.Mutex
	client  pricingCommandConnection
	key     string
	closed  bool
	factory func(pricingtransport.Config) (pricingCommandConnection, error) // local test seam
}

// The connection identity includes credential values only through a digest.
// Neither this key nor configuration material is emitted as diagnostics.
func (s *Server) pricingCommandFingerprint(cfg appconfig.Config) string {
	if strings.TrimSpace(cfg.Canonical.Pricing.Digitalogic.CommandWebSocketURL) == "" {
		return ""
	}
	source := canonical.SourceIdentity(s.currentDBPath(), cfg.Canonical.SourceID, "")
	return pricingCommandKey(cfg, source, strings.TrimSpace(os.Getenv(cfg.SendUpdates.ProductSyncSecretEnv)), strings.TrimSpace(os.Getenv(cfg.Canonical.Pricing.Digitalogic.BearerTokenEnv)))
}

func pricingCommandKey(cfg appconfig.Config, source canonical.Source, secret, token string) string {
	material, _ := json.Marshal([]any{cfg.Canonical.Pricing, cfg.SendUpdates.URL, cfg.SendUpdates.Timeout, source.ID, source.Dataset,
		secret, token})
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:])
}

func commandOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

func commandEndpoints(cfg appconfig.Config) (*url.URL, error) {
	d := pricingcatalog.Normalize(cfg.Canonical.Pricing).Digitalogic
	if pricingcatalog.Normalize(cfg.Canonical.Pricing).Mode != pricingcatalog.ModeDigitalogic {
		return nil, errPricingCommandsUnavailable
	}
	ws, e1 := url.Parse(d.CommandWebSocketURL)
	base, e2 := url.Parse(strings.TrimRight(d.BaseURL, "/") + "/")
	send, e3 := url.Parse(cfg.SendUpdates.URL)
	if e1 != nil || e2 != nil || e3 != nil {
		return nil, errPricingCommandsUnavailable
	}
	for _, u := range []*url.URL{ws, base, send} {
		if u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return nil, errPricingCommandsUnavailable
		}
	}
	if ws.Scheme != "wss" || base.Scheme != "https" || send.Scheme != "https" || commandOrigin(ws) != commandOrigin(base) || commandOrigin(ws) != commandOrigin(send) {
		return nil, errPricingCommandsUnavailable
	}
	path := strings.TrimSpace(d.CatalogPath)
	relative, err := url.Parse(path)
	if err != nil || path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) || relative.IsAbs() || relative.Host != "" || relative.User != nil || relative.RawQuery != "" || relative.Fragment != "" {
		return nil, errPricingCommandsUnavailable
	}
	catalog := base.ResolveReference(relative)
	if commandOrigin(catalog) != commandOrigin(base) || catalog.Scheme != base.Scheme {
		return nil, errPricingCommandsUnavailable
	}
	return catalog, nil
}

func (s *Server) pricingCommandClient(cfg appconfig.Config) (pricingCommandConnection, error) {
	if _, err := commandEndpoints(cfg); err != nil {
		return nil, err
	}
	secret, err := updateout.ResolveProductSyncSecret(updateout.Normalize(cfg.SendUpdates))
	if err != nil {
		return nil, errPricingCommandsUnavailable
	}
	d := pricingcatalog.Normalize(cfg.Canonical.Pricing).Digitalogic
	token := strings.TrimSpace(os.Getenv(d.BearerTokenEnv))
	if token == "" || secret == "" {
		return nil, errPricingCommandsUnavailable
	}
	source := canonical.SourceIdentity(s.currentDBPath(), cfg.Canonical.SourceID, "")
	key := pricingCommandKey(cfg, source, secret, token)
	state := &s.pricingCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil, errPricingCommandsUnavailable
	}
	if state.client != nil && state.key == key {
		return state.client, nil
	}
	if state.client != nil {
		_ = state.client.Close()
		state.client = nil
		state.key = ""
	}
	timeout, _ := time.ParseDuration(cfg.SendUpdates.Timeout)
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	transportConfig := pricingtransport.Config{URL: d.CommandWebSocketURL, Secret: secret, SourceID: source.ID, SourceDataset: source.Dataset, OwnerToken: token, Timeout: timeout}
	var client pricingCommandConnection
	if state.factory != nil {
		client, err = state.factory(transportConfig)
	} else {
		client, err = pricingtransport.New(transportConfig)
	}
	if err != nil {
		return nil, errPricingCommandsUnavailable
	}
	state.client, state.key = client, key
	return client, nil
}

func (s *Server) closePricingCommands() {
	state := &s.pricingCommands
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closed = true
	if state.client != nil {
		_ = state.client.Close()
		state.client = nil
	}
}

type pricingCommandRoundTripper struct {
	server *Server
	cfg    appconfig.Config
}

func (t pricingCommandRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	catalog, err := commandEndpoints(t.cfg)
	if err != nil {
		return nil, err
	}
	// Only the exact configured catalog GET is translated into a command.
	// Assignment reads retain their normal HTTP behavior and authentication.
	if request.Method != http.MethodGet || request.URL.String() != catalog.String() {
		if request.URL.Scheme != "https" || commandOrigin(request.URL) != commandOrigin(catalog) {
			return nil, errPricingCommandsUnavailable
		}
		return http.DefaultTransport.RoundTrip(request)
	}
	token := strings.TrimSpace(os.Getenv(t.cfg.Canonical.Pricing.Digitalogic.BearerTokenEnv))
	if token == "" || request.Header.Get("Authorization") != "Bearer "+token {
		return nil, errPricingCommandsUnavailable
	}
	client, err := t.server.pricingCommandClient(t.cfg)
	if err != nil {
		return nil, err
	}
	data, err := client.Catalog(request.Context())
	if err != nil {
		return nil, errPricingCommandsUnavailable
	}
	body, err := json.Marshal(struct {
		Data json.RawMessage `json:"data"`
	}{data})
	if err != nil {
		return nil, errPricingCommandsUnavailable
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}, nil
}

func (s *Server) pricingCommandHTTPClient(cfg appconfig.Config) *http.Client {
	if strings.TrimSpace(cfg.Canonical.Pricing.Digitalogic.CommandWebSocketURL) == "" {
		s.pricingCommands.mu.Lock()
		if s.pricingCommands.client != nil {
			_ = s.pricingCommands.client.Close()
			s.pricingCommands.client = nil
			s.pricingCommands.key = ""
		}
		s.pricingCommands.mu.Unlock()
		return nil
	}
	timeout, _ := time.ParseDuration(pricingcatalog.Normalize(cfg.Canonical.Pricing).Digitalogic.Timeout)
	return &http.Client{Timeout: timeout, Transport: pricingCommandRoundTripper{s, cfg}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Install the explicit exchange even when validation fails: an invalid command
// configuration must fail closed rather than silently send the write over HTTP.
func (s *Server) pricingCommandContext(ctx context.Context, cfg appconfig.Config, delivery updateout.Config) context.Context {
	if strings.TrimSpace(cfg.Canonical.Pricing.Digitalogic.CommandWebSocketURL) == "" {
		return ctx
	}
	return updateout.WithProductSyncExchange(ctx, func(ctx context.Context, envelope []byte) ([]byte, error) {
		if delivery.URL != cfg.SendUpdates.URL || delivery.ProductSyncSecretEnv != cfg.SendUpdates.ProductSyncSecretEnv {
			return nil, errPricingCommandsUnavailable
		}
		var wire struct {
			Source canonical.Source `json:"source"`
		}
		if json.Unmarshal(envelope, &wire) != nil {
			return nil, errPricingCommandsUnavailable
		}
		expected := canonical.SourceIdentity(s.currentDBPath(), cfg.Canonical.SourceID, "")
		if wire.Source.ID != expected.ID || wire.Source.Dataset != expected.Dataset {
			return nil, errPricingCommandsUnavailable
		}
		client, err := s.pricingCommandClient(cfg)
		if err != nil {
			return nil, err
		}
		data, err := client.Receive(ctx, envelope)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
		}{true, data})
	})
}
