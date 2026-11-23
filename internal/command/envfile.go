package command

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

var envFileKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseEnvFile reads KEY=VALUE lines. Blank lines and lines starting with #
// are ignored, an "export " prefix is allowed, and values may be wrapped in
// single or double quotes. No variable expansion happens.
func parseEnvFile(data []byte) (map[string]string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envFileKeyRe.MatchString(key) {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", n)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	return out, sc.Err()
}
