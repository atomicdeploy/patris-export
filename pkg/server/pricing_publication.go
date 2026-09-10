package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/appconfig"
	"github.com/atomicdeploy/patris-export/pkg/canonical"
	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
	"github.com/atomicdeploy/patris-export/pkg/recordpipe"
)

var errPricingAuthorityUnavailable = errors.New("pricing authority is unavailable")
var errPricingSnapshotDisabled = errors.New("snapshot_disabled")

var errPricingProjectionUnavailable = errors.New("verified owner pricing projection is unavailable")
var ownerNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// Codes are fixed boundary names; no source records or transport errors are exposed.
func pricingProjectionFailure(code string) error {
	return &excelPricingRemoteSnapshotStageError{stage: "owner_projection", code: code, cause: errPricingProjectionUnavailable}
}

func (s *Server) selectedPricingOwner(ctx context.Context, cfg appconfig.Config) (pricingcatalog.Resolution, error) {
	// Standalone row conversion has no configured price publisher to select.
	if !pricingcatalog.Configured(cfg.Canonical.Pricing) {
		return pricingcatalog.Resolution{Authority: pricingcatalog.AuthorityGo}, nil
	}
	provider, ok := s.pricingCatalogProvider(cfg).(pricingcatalog.OwnerProvider)
	if !ok {
		return pricingcatalog.Resolution{}, errPricingAuthorityUnavailable
	}
	owner := provider.Owner(ctx)
	if owner.AuthorityError != "" || (owner.Authority != pricingcatalog.AuthorityPHP && owner.Authority != pricingcatalog.AuthorityGo) ||
		(owner.CatalogStatus != "fresh" && owner.CatalogStatus != "static") {
		return pricingcatalog.Resolution{}, errPricingAuthorityUnavailable
	}
	return owner, nil
}

func (s *Server) canonicalPublicationResultContext(ctx context.Context) (recordpipe.Result, error) {
	build := func(ctx context.Context) (recordpipe.Result, error) {
		input, err := s.canonicalRecordResultContext(ctx)
		if err != nil || input.Contract == nil {
			return input, err
		}
		cfg := s.Config()
		owner, err := s.selectedPricingOwner(ctx, cfg)
		if err != nil {
			return recordpipe.Result{}, err
		}
		if owner.Authority == pricingcatalog.AuthorityPHP && s.pricingActuation != nil {
			latest := s.pricingActuation.status().LatestSource
			if validExcelPricingRemoteSource(latest) {
				return s.projectPricingFinal(ctx, input, latest, owner)
			}
		}
		return s.projectPricingInput(ctx, input, cfg, owner)
	}
	if s.pricingPublication == nil {
		return build(ctx)
	}
	return s.pricingPublication.get(ctx, func() time.Duration { return canonicalProjectionMaxAge(s.Config()) }, build)
}

// An authenticated owner event names the existing final source. Refresh that
// projection directly: owner settings changes do not require input redelivery.
func (s *Server) projectPricingFinal(ctx context.Context, input recordpipe.Result, source canonical.Source, owner pricingcatalog.Resolution) (recordpipe.Result, error) {
	// The event is an invalidation hint. Read the currently committed owner
	// source, bound to the current input, instead of reviving an old snapshot.
	return s.projectPricingInput(ctx, input, s.Config(), owner)
}

func (s *Server) projectPricingInput(ctx context.Context, input recordpipe.Result, cfg appconfig.Config, owner pricingcatalog.Resolution) (recordpipe.Result, error) {
	if pricingcatalog.Configured(cfg.Canonical.Pricing) {
		if input.PricingAuthority != owner.Authority {
			return recordpipe.Result{}, pricingProjectionFailure("input_authority_mismatch")
		}
		expectedMode := canonical.InputModeGoProjection
		if owner.Authority == pricingcatalog.AuthorityPHP {
			expectedMode = canonical.InputModePatrisInputs
		}
		if input.Contract.InputMode != expectedMode {
			return recordpipe.Result{}, pricingProjectionFailure("input_mode_mismatch")
		}
		if owner.Authority == pricingcatalog.AuthorityGo {
			for _, product := range input.Contract.Products {
				if product.PricingCatalogRevision != owner.CatalogRevision {
					return recordpipe.Result{}, pricingProjectionFailure("input_catalog_revision_mismatch")
				}
			}
		}
	}
	if owner.Authority == pricingcatalog.AuthorityGo {
		return input, nil
	}
	return s.readCurrentOwnerProjection(ctx, input, cfg, owner)
}

func (s *Server) readCurrentOwnerProjection(ctx context.Context, input recordpipe.Result, cfg appconfig.Config, owner pricingcatalog.Resolution) (recordpipe.Result, error) {
	var wire struct {
		Data struct {
			Schema    string            `json:"schema"`
			Authority string            `json:"authority"`
			Revision  string            `json:"owner_catalog_revision"`
			Input     canonical.Source  `json:"input_source"`
			Source    canonical.Source  `json:"source"`
			Count     int               `json:"row_count"`
			Rows      []json.RawMessage `json:"rows"`
		} `json:"data"`
	}
	query := url.Values{"projection": {"current-products"}, "source_id": {input.Contract.Source.ID}, "source_dataset": {input.Contract.Source.Dataset}, "source_revision": {input.Contract.Source.Revision}, "owner_catalog_revision": {owner.CatalogRevision}}
	if err := pricingcatalog.ReadCurrentOwnerProducts(ctx, cfg.Canonical.Pricing.Digitalogic, query, &wire.Data); err != nil {
		return recordpipe.Result{}, pricingProjectionFailure("owner_read_rejected")
	}
	if wire.Data.Schema != "digitalogic.current-owner-products.v1" || wire.Data.Authority != pricingcatalog.AuthorityPHP || wire.Data.Revision != owner.CatalogRevision || !wire.Data.Input.SameIdentity(input.Contract.Source) || wire.Data.Count != len(wire.Data.Rows) {
		return recordpipe.Result{}, pricingProjectionFailure("owner_binding_mismatch")
	}
	return ownerProductProjection(input.Contract, &excelPricingRemoteSnapshotResult{Source: wire.Data.Source, Rows: wire.Data.Rows}, owner.CatalogRevision)
}

func ownerProductProjection(input *canonical.Envelope, remote *excelPricingRemoteSnapshotResult, ownerRevision string) (recordpipe.Result, error) {
	if input == nil || remote == nil || remote.Source.ID != input.Source.ID || remote.Source.Dataset != input.Source.Dataset {
		return recordpipe.Result{}, pricingProjectionFailure("source_identity_mismatch")
	}
	expected := make(map[string]bool, len(input.Products))
	for _, product := range input.Products {
		expected[product.ProductCode] = false
	}
	products := make([]canonical.Product, 0, len(expected))
	for _, row := range remote.Rows {
		var wire struct {
			Code    string          `json:"patris_code"`
			Product json.RawMessage `json:"canonical_product"`
		}
		if json.Unmarshal(row, &wire) != nil {
			return recordpipe.Result{}, pricingProjectionFailure("row_json_invalid")
		}
		if wire.Code == "" {
			if len(wire.Product) != 0 && !bytes.Equal(bytes.TrimSpace(wire.Product), []byte("null")) {
				return recordpipe.Result{}, pricingProjectionFailure("canonical_product_without_code")
			}
			continue
		}
		seen, exists := expected[wire.Code]
		if !exists {
			return recordpipe.Result{}, pricingProjectionFailure("unexpected_product_code")
		}
		if seen {
			return recordpipe.Result{}, pricingProjectionFailure("duplicate_product_code")
		}
		normalized, err := normalizeOwnerProductNumbers(wire.Product)
		if err != nil {
			return recordpipe.Result{}, pricingProjectionFailure("canonical_product_numbers_invalid")
		}
		product, err := canonical.VerifyProductJSON(normalized)
		if err != nil {
			return recordpipe.Result{}, pricingProjectionFailure("canonical_product_verification_failed")
		}
		if product.ProductCode != wire.Code {
			return recordpipe.Result{}, pricingProjectionFailure("canonical_product_code_mismatch")
		}
		if (product.FinalPrice != nil || product.PricingCatalogRevision != "") && product.PricingCatalogRevision != ownerRevision {
			return recordpipe.Result{}, pricingProjectionFailure("canonical_product_catalog_mismatch")
		}
		products = append(products, product)
		expected[wire.Code] = true
	}
	if len(products) != len(expected) {
		return recordpipe.Result{}, pricingProjectionFailure("missing_product_codes")
	}
	// Recompute only to verify the owner's existing source hash; retain each
	// transported product hash and use the authenticated final source identity.
	verified := canonical.NewCatalogEnvelope(products, input.Categories, input.ExcludedCodes, input.Source.Dataset, input.Source.ID, time.Now(), input.QuarantinedCodes...)
	if !verified.Source.SameIdentity(remote.Source) {
		return recordpipe.Result{}, pricingProjectionFailure("final_source_hash_mismatch")
	}
	verified.Source = remote.Source
	rows := canonical.ProductsToRows(verified.Products)
	return recordpipe.Result{Contract: verified, Rows: rows, Payload: rows, KeyField: "product_code", PricingAuthority: pricingcatalog.AuthorityPHP, PricingInputSource: input.Source, OwnerCatalogRevision: ownerRevision}, nil
}

func normalizeOwnerProductNumbers(data []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, errPricingProjectionUnavailable
	}
	normalize := func(value json.RawMessage) (json.RawMessage, error) {
		if len(value) == 0 || value[0] != '"' {
			return value, nil
		}
		var text string
		if json.Unmarshal(value, &text) != nil || !ownerNumberPattern.MatchString(text) {
			return nil, errPricingProjectionUnavailable
		}
		return json.RawMessage(text), nil
	}
	for _, field := range []string{"sale_price_source", "partner_price_source", "purchase_price_source", "total_stock", "minimum_stock", "foreign_price", "weight_grams", "shipping_price_per_kg", "markup_percent", "irt_per_cny", "price_source_amount", "price_rounding_digits", "final_price"} {
		if value, ok := fields[field]; ok {
			var err error
			fields[field], err = normalize(value)
			if err != nil {
				return nil, err
			}
		}
	}
	if stock := fields["warehouse_stock"]; len(stock) > 0 && !bytes.Equal(stock, []byte("null")) {
		var warehouses map[string]json.RawMessage
		if json.Unmarshal(stock, &warehouses) != nil {
			return nil, errPricingProjectionUnavailable
		}
		for key, value := range warehouses {
			var err error
			warehouses[key], err = normalize(value)
			if err != nil {
				return nil, err
			}
		}
		fields["warehouse_stock"], _ = json.Marshal(warehouses)
	}
	return json.Marshal(fields)
}
