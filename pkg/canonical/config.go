package canonical

import (
	"net/url"
	"path"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

const ProfileKala = "kala"

type Config struct {
	Enabled  bool                     `json:"enabled" yaml:"enabled" toml:"enabled"`
	SourceID string                   `json:"source_id,omitempty" yaml:"source_id,omitempty" toml:"source_id,omitempty"`
	Profiles map[string]ProfileConfig `json:"profiles,omitempty" yaml:"profiles,omitempty" toml:"profiles,omitempty"`
	Pricing  pricingcatalog.Config    `json:"pricing,omitempty" yaml:"pricing,omitempty" toml:"pricing,omitempty"`
	Hashes   HashConfig               `json:"hashes,omitempty" yaml:"hashes,omitempty" toml:"hashes,omitempty"`
}

type ProfileConfig struct {
	Type    string `json:"type" yaml:"type" toml:"type"`
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"`
}

// HashConfig separates compatibility identities from ordinary presentation.
// Enabled controls calculation and publication of record, source, and event
// hashes; disabling it also makes the hash-dependent product-sync adapter
// unavailable. Expose controls whether per-record hashes appear in ordinary
// exports and collection endpoints while internal compatibility identities
// remain available.
type HashConfig struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"`
	Expose  *bool `json:"expose,omitempty" yaml:"expose,omitempty" toml:"expose,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Enabled: true,
		Profiles: map[string]ProfileConfig{
			"kala.db": {Type: ProfileKala},
		},
		Pricing: pricingcatalog.DefaultConfig(),
		Hashes: HashConfig{
			Enabled: boolPointer(true),
			Expose:  boolPointer(true),
		},
	}
}

func NormalizeConfig(cfg Config) Config {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	profiles := make(map[string]ProfileConfig, len(cfg.Profiles))
	for source, profile := range cfg.Profiles {
		source = sourceBaseName(source)
		profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
		if source != "" && profile.Type != "" {
			profiles[source] = profile
		}
	}
	cfg.Profiles = profiles
	cfg.Pricing = pricingcatalog.Normalize(cfg.Pricing)
	if cfg.Hashes.Enabled == nil {
		cfg.Hashes.Enabled = boolPointer(true)
	}
	if cfg.Hashes.Expose == nil {
		cfg.Hashes.Expose = boolPointer(true)
	}
	if !*cfg.Hashes.Enabled {
		cfg.Hashes.Expose = boolPointer(false)
	}
	return cfg
}

// HashesEnabled reports whether hash-dependent compatibility surfaces such as
// product-sync should be published. A missing setting keeps the historic
// enabled behavior for programmatic callers that construct Config directly.
func (cfg Config) HashesEnabled() bool {
	return cfg.Hashes.Enabled == nil || *cfg.Hashes.Enabled
}

// ExposeRecordHashes reports whether ordinary rows should carry record_hash.
// Hash publication must be enabled before the per-record identities can be
// exposed.
func (cfg Config) ExposeRecordHashes() bool {
	if !cfg.HashesEnabled() {
		return false
	}
	return cfg.Hashes.Expose == nil || *cfg.Hashes.Expose
}

func boolPointer(value bool) *bool {
	return &value
}

func ProfileFor(source string, cfg Config) (ProfileConfig, bool) {
	if !cfg.Enabled {
		return ProfileConfig{}, false
	}
	cfg = NormalizeConfig(cfg)
	name := sourceBaseName(source)
	profile, ok := cfg.Profiles[name]
	if !ok {
		profile, ok = cfg.Profiles["*"]
	}
	if !ok || (profile.Enabled != nil && !*profile.Enabled) {
		return ProfileConfig{}, false
	}
	return profile, profile.Type == ProfileKala
}

func sourceBaseName(source string) string {
	trimmed := strings.TrimSpace(source)
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && strings.Contains(trimmed, "://") {
		trimmed = parsed.Path
	}
	// Configuration may be authored on a different operating system than the
	// current process. Treat both slash styles as separators so a Windows path
	// keeps the same dataset identity on Linux CI and vice versa.
	base := strings.ToLower(path.Base(strings.ReplaceAll(trimmed, `\`, "/")))
	if base == "" || base == "." {
		return "patris-export"
	}
	return base
}
