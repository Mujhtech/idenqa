package review

import (
	"slices"
	"time"
)

// PolicySettings is pinned into every newly routed case.
type PolicySettings struct {
	RoutingRule
	Display               []DisplayRule `json:"display"`
	Priority              int           `json:"priority"`
	SLASeconds            int64         `json:"sla_seconds"`
	Language              string        `json:"language"`
	Reason                string        `json:"reason"`
	Assurance             string        `json:"assurance"`
	Risk                  string        `json:"risk"`
	SamplePercent         int           `json:"sample_percent"`
	AppealWindowSeconds   int64         `json:"appeal_window_seconds"`
	EscalationCertificate string        `json:"escalation_certificate"`
}

// Validate checks the bounded configuration before it is persisted.
func (s PolicySettings) Validate() error {
	if s.Priority < 0 || s.Priority > 9 || s.SLASeconds < 60 || s.SLASeconds > 2592000 || s.SamplePercent < 0 || s.SamplePercent > 100 || s.AppealWindowSeconds < 60 || s.AppealWindowSeconds > 31536000 || !authorityLabel(s.RequiredCertificate) || !authorityLabel(s.EscalationCertificate) || !slices.Contains([]Oversight{OversightSingle, OversightDual}, s.Oversight) || ValidateFindingRules(s.PermittedFindings) != nil || len(s.Display) > 32 {
		return ErrInvalid
	}
	for _, label := range []string{s.Language, s.Reason, s.Assurance, s.Risk} {
		if !authorityLabel(label) {
			return ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, rule := range s.Display {
		if rule.Validate() != nil || seen[rule.Requirement] {
			return ErrInvalid
		}
		seen[rule.Requirement] = true
	}
	return nil
}

// QueueQuery contains only bounded, exact-value filters and a stable position.
type QueueQuery struct {
	State         string
	Region        string
	Reviewer      string
	Language      string
	Reason        string
	Assurance     string
	Risk          string
	Certificate   string
	Sampled       *bool
	Overdue       *bool
	Limit         int
	AfterPriority int
	AfterDue      time.Time
	AfterID       string
	At            time.Time
}

// QueueItem exposes operational metadata without subject attributes or evidence.
type QueueItem struct {
	Case      Case
	Version   int64
	Priority  int
	DueAt     *time.Time
	SortAt    time.Time
	Language  string
	Reason    string
	Assurance string
	Risk      string
	Sampled   bool
}
