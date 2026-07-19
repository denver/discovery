package config

import (
	"bufio"
	"os"
	"strings"
)

// EnvLookup returns the value for key: the process environment when set
// and non-empty, otherwise the value from ./.env when present. This is
// how all Discovery configuration is read — `go run` and the binaries
// work out of the box after `cp .env.example .env`.
func EnvLookup(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return dotenv()[key]
}

// dotenv parses ./.env in the current directory. A missing or unreadable
// file is simply an empty map: .env is a convenience, never a requirement.
func dotenv() map[string]string {
	f, err := os.Open(".env")
	if err != nil {
		return nil
	}
	defer f.Close()

	vars := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			vars[key] = val
		}
	}
	return vars
}
