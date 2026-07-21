package pricingcatalog

import (
	"bytes"
	"encoding/json"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// shippingPairPresence combines source-level key presence with values supplied
// directly by Go callers. Source presence matters because an explicit null is
// data, while an absent key is not.
func (method Method) shippingPairPresence() (bool, bool) {
	return method.pricePerKgPresent || method.PricePerKg != nil,
		method.currencyPresent || method.Currency != ""
}

func (method *Method) setShippingPairPresence(pricePresent, priceNull, currencyPresent, currencyNull bool) {
	method.pricePerKgPresent = pricePresent
	method.pricePerKgNull = pricePresent && priceNull
	method.currencyPresent = currencyPresent
	method.currencyNull = currencyPresent && currencyNull
}

func (method *Method) UnmarshalJSON(data []byte) error {
	type plain Method
	var decoded plain
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*method = Method(decoded)
	price, pricePresent := raw["price_per_kg"]
	currency, currencyPresent := raw["currency"]
	method.setShippingPairPresence(
		pricePresent,
		pricePresent && bytes.Equal(bytes.TrimSpace(price), []byte("null")),
		currencyPresent,
		currencyPresent && bytes.Equal(bytes.TrimSpace(currency), []byte("null")),
	)
	return nil
}

func (method Method) MarshalJSON() ([]byte, error) {
	return json.Marshal(method.sourceMap())
}

func (method *Method) UnmarshalYAML(node *yaml.Node) error {
	type plain Method
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*method = Method(decoded)
	pricePresent, priceNull := yamlMappingPresence(node, "price_per_kg")
	currencyPresent, currencyNull := yamlMappingPresence(node, "currency")
	method.setShippingPairPresence(pricePresent, priceNull, currencyPresent, currencyNull)
	return nil
}

func (method Method) MarshalYAML() (interface{}, error) {
	return method.sourceMap(), nil
}

// UnmarshalTOML records key presence for the TOML configuration path. TOML
// itself has no null literal, but retaining presence still prevents an omitted
// key from being confused with a supplied (possibly invalid) value.
func (method *Method) UnmarshalTOML(data []byte) error {
	type plain Method
	var decoded plain
	if err := toml.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]interface{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return err
	}
	*method = Method(decoded)
	_, pricePresent := raw["price_per_kg"]
	_, currencyPresent := raw["currency"]
	method.setShippingPairPresence(pricePresent, false, currencyPresent, false)
	return nil
}

func (method Method) sourceMap() map[string]interface{} {
	result := map[string]interface{}{"id": method.ID}
	if method.Name != "" {
		result["name"] = method.Name
	}
	if method.Enabled != nil {
		result["enabled"] = method.Enabled
	}
	pricePresent, currencyPresent := method.shippingPairPresence()
	if method.PricePerKg != nil {
		result["price_per_kg"] = method.PricePerKg
	} else if pricePresent && method.pricePerKgNull {
		result["price_per_kg"] = nil
	}
	if method.Currency != "" || (currencyPresent && !method.currencyNull) {
		result["currency"] = method.Currency
	} else if currencyPresent && method.currencyNull {
		result["currency"] = nil
	}
	return result
}

func yamlMappingPresence(node *yaml.Node, field string) (bool, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return false, false
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == field {
			value := node.Content[index+1]
			return true, value.Tag == "!!null"
		}
	}
	return false, false
}
