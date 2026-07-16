package canonical

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/atomicdeploy/patris-export/pkg/pricingcatalog"
)

const ProfileKalaV1 = "kala_v1"

type Config struct {
	Enabled  bool                     `json:"enabled" yaml:"enabled" toml:"enabled"`
	SourceID string                   `json:"source_id,omitempty" yaml:"source_id,omitempty" toml:"source_id,omitempty"`
	Profiles map[string]ProfileConfig `json:"profiles,omitempty" yaml:"profiles,omitempty" toml:"profiles,omitempty"`
	Pricing  pricingcatalog.Config    `json:"pricing,omitempty" yaml:"pricing,omitempty" toml:"pricing,omitempty"`
}

type ProfileConfig struct {
	Type    string `json:"type" yaml:"type" toml:"type"`
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty" toml:"enabled,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Enabled: true,
		Profiles: map[string]ProfileConfig{
			"kala.db": {Type: ProfileKalaV1},
		},
		Pricing: pricingcatalog.DefaultConfig(),
	}
}

func NormalizeConfig(cfg Config) Config {
	cfg.SourceID = strings.TrimSpace(cfg.SourceID)
	profiles := make(map[string]ProfileConfig, len(cfg.Profiles))
	for source, profile := range cfg.Profiles {
		source = strings.ToLower(strings.TrimSpace(filepath.Base(source)))
		profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
		if source != "" && profile.Type != "" {
			profiles[source] = profile
		}
	}
	cfg.Profiles = profiles
	cfg.Pricing = pricingcatalog.Normalize(cfg.Pricing)
	return cfg
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
	return profile, profile.Type == ProfileKalaV1
}

func sourceBaseName(source string) string {
	trimmed := strings.TrimSpace(source)
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && strings.Contains(trimmed, "://") {
		trimmed = parsed.Path
	}
	base := strings.ToLower(filepath.Base(trimmed))
	if base == "" || base == "." {
		return "patris-export"
	}
	return base
}
