package frontend

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// coding is a content coding SvelteKit writes a precompressed sidecar for, with the sidecar's file extension
type coding struct{ name, ext string }

// precompressed lists the sidecar codings in the order the server prefers them when the client weighs them equally
var precompressed = []coding{{"br", ".br"}, {"gzip", ".gz"}}

// acceptedCodings returns the precompressed codings the Accept-Encoding header values allow, the client's most preferred first
// A coding the client weighs with q=0, or leaves out without a * entry, is not allowed
func acceptedCodings(header []string) []coding {
	// Weigh every listed coding by its quality value
	weights := map[string]float64{}
	for _, value := range header {
		for entry := range strings.SplitSeq(value, ",") {
			name, params, _ := strings.Cut(entry, ";")
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" {
				weights[name] = quality(params)
			}
		}
	}
	weight := func(c coding) float64 {
		if q, ok := weights[c.name]; ok {
			return q
		}
		return weights["*"]
	}

	// Rank the allowed codings by weight, and keep the server's order on a tie
	accepted := slices.DeleteFunc(slices.Clone(precompressed), func(c coding) bool { return weight(c) <= 0 })
	slices.SortStableFunc(accepted, func(a, b coding) int { return cmp.Compare(weight(b), weight(a)) })
	return accepted
}

// quality is the q parameter of an Accept-Encoding entry, where a missing one means 1 and a malformed one rules the coding out
func quality(params string) float64 {
	for param := range strings.SplitSeq(params, ";") {
		key, value, _ := strings.Cut(param, "=")
		if !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || q < 0 || q > 1 {
			return 0
		}
		return q
	}
	return 1
}
