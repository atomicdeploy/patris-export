package pricingcatalog

import (
	"bytes"
	"encoding/json"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// StaticConfig records rounding_digits key presence independently from its
// value. A supplied null is reference data and must not collapse into the
// omitted/default case.
func (config *StaticConfig) UnmarshalJSON(data []byte) error {
	type plain StaticConfig
	var decoded plain
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*config = StaticConfig(decoded)
	value, present := raw["rounding_digits"]
	config.roundingDigitsPresent = present
	config.roundingDigitsNull = present && bytes.Equal(bytes.TrimSpace(value), []byte("null"))
	return nil
}

func (config StaticConfig) MarshalJSON() ([]byte, error) {
	type plain StaticConfig
	data, err := json.Marshal(plain(config))
	if err != nil {
		return nil, err
	}
	if !config.roundingDigitsPresent || !config.roundingDigitsNull {
		return data, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	raw["rounding_digits"] = json.RawMessage("null")
	return json.Marshal(raw)
}

func (config *StaticConfig) UnmarshalYAML(node *yaml.Node) error {
	type plain StaticConfig
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*config = StaticConfig(decoded)
	present, isNull := yamlMappingPresence(node, "rounding_digits")
	config.roundingDigitsPresent = present
	config.roundingDigitsNull = present && isNull
	return nil
}

func (config StaticConfig) MarshalYAML() (interface{}, error) {
	type plain StaticConfig
	data, err := yaml.Marshal(plain(config))
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if config.roundingDigitsPresent && config.roundingDigitsNull {
		raw["rounding_digits"] = nil
	}
	return raw, nil
}

func (config *StaticConfig) UnmarshalTOML(data []byte) error {
	type plain StaticConfig
	var decoded plain
	if err := toml.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]interface{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return err
	}
	*config = StaticConfig(decoded)
	_, config.roundingDigitsPresent = raw["rounding_digits"]
	config.roundingDigitsNull = false
	return nil
}
