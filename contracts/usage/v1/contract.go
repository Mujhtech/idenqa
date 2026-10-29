// Package usagev1 defines content-free, regional Core usage receipts. A receipt
// establishes a durable Core event; it does not establish a provider charge.
package usagev1

import "time"

// Schema identifies the regional usage receipt wire contract.
const Schema = "idenqa-core.usage-receipt.v1"

// Coverage proves a complete acknowledged interval for one immutable regional
// consumer/routing registration. It cannot imply historical backfill.
type Coverage struct {
	Schema        string    `json:"schema"`
	CoreTenantID  string    `json:"coreTenantId"`
	ConsumerID    string    `json:"consumerId"`
	RoutingDigest string    `json:"routingDigest"`
	Epoch         string    `json:"epoch"`
	From          time.Time `json:"from"`
	Through       time.Time `json:"through"`
}

// Receipt establishes one durable, content-free Core event.
type Receipt struct {
	Schema         string    `json:"schema"`
	ID             string    `json:"id"`
	CoreTenantID   string    `json:"coreTenantId"`
	Event          string    `json:"event"`
	ProviderID     string    `json:"providerId"`
	AdapterID      string    `json:"adapterId"`
	AdapterVersion string    `json:"adapterVersion"`
	PackageDigest  string    `json:"packageDigest"`
	Check          string    `json:"check"`
	OccurredAt     time.Time `json:"occurredAt"`
	Quantity       int64     `json:"quantity"`
	ModelID        string    `json:"modelId,omitempty"`
}
