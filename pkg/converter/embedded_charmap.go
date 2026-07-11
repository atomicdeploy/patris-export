package converter

import (
	_ "embed"
	"fmt"
	"strings"
)

// embeddedCharMapText is the built-in Patris81 -> modern text conversion map.
// It is the default mapping used by the app when the user does not pass -m/--charmap.
//
//go:embed farsi_chars.txt
var embeddedCharMapText string

func init() {
	mapping, err := parseCharMapping(strings.NewReader(embeddedCharMapText))
	if err != nil {
		panic(fmt.Sprintf("failed to load embedded Patris81 character map: %v", err))
	}
	SetDefaultMapping(mapping)
}
