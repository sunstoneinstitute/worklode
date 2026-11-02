package critjson

import (
	_ "embed"
	"strings"
)

//go:embed PIN
var pin string

// PinnedTag is the crit tag PIN names, such as "v0.23.2".
func PinnedTag() string {
	f := strings.Fields(pin)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
