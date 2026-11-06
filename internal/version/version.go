package version

import (
	"encoding/json"
	"strconv"
	"strings"

	featherlesscloud "featherless.ai/featherless-cloud"
)

const AppName = "featherless-cloud"

var (
	PackageName string
	Version     string
)

func init() {
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}

	if err := json.Unmarshal(featherlesscloud.PackageJSON, &pkg); err != nil {
		panic("package.json: " + err.Error())
	}

	PackageName = pkg.Name
	Version = pkg.Version
}

func IsNewer(candidate, current string) bool {
	a := parts(candidate)
	b := parts(current)

	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}

	return false
}

func parts(version string) [3]int {
	var out [3]int

	for i, field := range strings.SplitN(version, ".", 3) {
		n, err := strconv.Atoi(field)
		if err == nil {
			out[i] = n
		}
	}

	return out
}

// FormatDollars renders micro-dollar amounts as dollars; sub-cent amounts keep
// full micro precision. Never shows micro units.
func FormatDollars(micros string) string {
	m, err := strconv.ParseFloat(micros, 64)
	if err != nil {
		m = 0
	}

	if m >= 10_000 {
		return "$" + strconv.FormatFloat(m/1e6, 'f', 2, 64)
	}

	dollars := strings.TrimRight(strconv.FormatFloat(m/1e6, 'f', 6, 64), "0")

	if strings.HasSuffix(dollars, ".") {
		dollars += "00"
	}

	return "$" + dollars
}
