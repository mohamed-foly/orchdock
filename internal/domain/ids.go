package domain

import (
	"crypto/rand"
	"regexp"
	"strings"
)

// ID prefixes.
const (
	PrefixProject  = "p_"
	PrefixItem     = "i_"
	PrefixProfile  = "ap_"
	PrefixTemplate = "t_"
	PrefixStack    = "st_"
	PrefixView     = "v_"
	PrefixRun      = "r_"
	PrefixKind     = "k_"
)

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// NewID returns prefix + 10 random characters from [0-9a-z], e.g. NewID("i_") = "i_7f3k2a9q0b".
func NewID(prefix string) string {
	const n = 10
	out := make([]byte, 0, len(prefix)+n)
	out = append(out, prefix...)
	buf := make([]byte, 1)
	for len(out) < len(prefix)+n {
		_, _ = rand.Read(buf)
		if buf[0] >= 252 { // 252 = 7*36: reject the tail to keep the distribution uniform
			continue
		}
		out = append(out, idAlphabet[int(buf[0])%len(idAlphabet)])
	}
	return string(out)
}

var idBody = regexp.MustCompile(`^[0-9a-z][0-9a-z_-]{0,47}$`)

// ValidID reports whether id is prefix followed by [0-9a-z_-] (generated ids
// have 10 characters; fixed ones such as "ap_claude" are allowed too).
func ValidID(prefix, id string) bool {
	return strings.HasPrefix(id, prefix) && idBody.MatchString(id[len(prefix):])
}

func checkID(c *collector, field, prefix, id string) {
	if !ValidID(prefix, id) {
		c.addf(field, "%q is not a valid id (want %s followed by lowercase letters, digits, '-' or '_')", id, prefix)
	}
}
