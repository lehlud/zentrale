package providers

import (
	"encoding/json"
	"testing"
)

func TestResponsesCacheUsageDistinguishesMissingZeroAndMixedWrites(t *testing.T) {
	for _, test := range []struct {
		name, usage        string
		known, read, write bool
		writes, longWrites int
	}{
		{"missing", `null`, false, false, false, 0, 0},
		{"incomplete-counts", `{"input_tokens":12000}`, false, false, false, 0, 0},
		{"older-response", `{"input_tokens":12000,"output_tokens":20,"input_tokens_details":{"cached_tokens":4000}}`, true, true, false, 0, 0},
		{"reported-zero", `{"input_tokens":12000,"output_tokens":20,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}`, true, true, true, 0, 0},
		{"mixed-writes", `{"input_tokens":12000,"output_tokens":20,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}`, true, true, true, 7000, 4000},
	} {
		t.Run(test.name, func(t *testing.T) {
			var usage any
			if err := json.Unmarshal([]byte(test.usage), &usage); err != nil {
				t.Fatal(err)
			}
			var result Result
			readResponsesUsage(&result, usage)
			if result.UsageKnown != test.known || result.CacheReadKnown != test.read || result.CacheWriteKnown != test.write || result.CacheWriteTokens != test.writes || result.CacheWriteLongTokens != test.longWrites {
				t.Fatalf("cache usage: %+v", result)
			}
		})
	}
}
