package v1

import "time"

// Summary contains only regional policy and counter metadata. It deliberately
// excludes operation identifiers, receipts, source records and signing material.
type Summary struct {
	Revision   int64            `json:"revision"`
	Status     string           `json:"status"`
	ValidFrom  time.Time        `json:"validFrom"`
	ValidUntil time.Time        `json:"validUntil"`
	ObservedAt time.Time        `json:"observedAt"`
	Limits     []CounterSummary `json:"limits"`
}

// CounterSummary describes one explicitly configured limit in its current
// fixed-duration window. Dirty counters require repair, never an inferred zero.
type CounterSummary struct {
	Kind                string `json:"kind"`
	Key                 string `json:"key"`
	Maximum             int64  `json:"maximum"`
	Used                int64  `json:"used"`
	Remaining           int64  `json:"remaining"`
	WindowStartUnixNano int64  `json:"windowStartUnixNano"`
	Dirty               bool   `json:"dirty"`
}
