package identity

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Command is a closed, authorized identity mutation. Personal data is transient.
type Command struct {
	Operation         string
	SubjectID         string
	ExpectedVersion   int64
	ExternalReference *string
	State             string
	VerificationID    string
	Record            *RecordInput
	Configuration     *Configuration
	Actor             id.APIKey
	Key               string
	At                time.Time
	Region            string
}

// Query selects bounded tenant-owned resources; reveals require a separate permission.
type Query struct {
	Kind              string
	SubjectID         string
	Reference         string
	After             string
	Limit             int
	RecordKind        string
	Current           bool
	Reveal            bool
	ExternalReference string
	Identifier        *Lookup
	Actor             id.APIKey
	At                time.Time
	Region            string
}

// Lookup resolves exact canonical identifiers without asserting subject equivalence.
type Lookup struct {
	Namespace     string `json:"namespace"`
	Issuer        string `json:"issuer"`
	Normalization string `json:"normalization"`
	Value         string `json:"value"`
}

// Result carries safe metadata unless a separate reveal permission was checked.
type Result struct {
	Subject         *Subject       `json:"subject,omitempty"`
	Record          *Record        `json:"record,omitempty"`
	Subjects        []Subject      `json:"subjects,omitzero"`
	Records         []Record       `json:"records,omitzero"`
	VerificationIDs []string       `json:"verification_ids,omitzero"`
	NextCursor      string         `json:"next_cursor,omitempty"`
	Version         int64          `json:"version,omitempty"`
	Configuration   *Configuration `json:"configuration,omitempty"`
	Receipt         *Receipt       `json:"receipt,omitempty"`
	Digest          string         `json:"digest,omitempty"`
	DeletionID      string         `json:"deletion_id,omitempty"`
}

// Repository consumes only owned application contracts, never HTTP or library types.
type Repository interface {
	Execute(context.Context, tenant.Scope, Command) (Result, error)
	Read(context.Context, tenant.Scope, Query) (Result, error)
}

// Service requires exact application permissions, including on direct calls.
type Service struct {
	repository Repository
	now        func() time.Time
	region     string
}

// NewService constructs the regional identity application boundary.
func NewService(r Repository, now func() time.Time, region string) (*Service, error) {
	if r == nil || now == nil || !validRegion(region) {
		return nil, ErrInvalid
	}
	return &Service{r, now, region}, nil
}

// Execute checks command meaning and preserves opaque tenant scope from authentication.
func (s *Service) Execute(ctx context.Context, auth access.Context, key string, c Command) (Result, error) {
	permission := access.PermissionSubjectsWrite
	switch c.Operation {
	case "create", "update", "link", "rebuild":
	case "delete":
		permission = access.PermissionSubjectsDelete
	case "record":
		permission = access.PermissionIdentityWrite
	case "configure":
		permission = access.PermissionIdentityConfigure
	default:
		return Result{}, ErrInvalid
	}
	if e := auth.Require(permission); e != nil {
		return Result{}, e
	}
	c.At = s.now().UTC().Truncate(time.Microsecond)
	c.Actor = auth.Principal().KeyID()
	c.Key = key
	c.Region = s.region
	if key == "" || len(key) > 200 || c.ExpectedVersion < 0 || c.ExpectedVersion == 9223372036854775807 {
		return Result{}, ErrInvalid
	}
	if c.Operation != "create" && c.Operation != "configure" {
		if _, e := id.ParseSubject(c.SubjectID); e != nil {
			return Result{}, ErrInvalid
		}
	}
	if c.ExternalReference != nil && (len(*c.ExternalReference) > 200 || (*c.ExternalReference != "" && (Value{Type: "string", Text: *c.ExternalReference}).Validate() != nil)) {
		return Result{}, ErrInvalid
	}
	switch c.Operation {
	case "create":
		if c.SubjectID != "" || c.ExpectedVersion != 0 || c.State != "" || c.Record != nil || c.Configuration != nil || c.VerificationID != "" {
			return Result{}, ErrInvalid
		}
	case "update":
		if (c.State != "" && c.State != "active" && c.State != "suspended") || c.Record != nil || c.Configuration != nil || c.VerificationID != "" || (c.State == "" && c.ExternalReference == nil) {
			return Result{}, ErrInvalid
		}
	case "link":
		if _, e := id.ParseVerification(c.VerificationID); e != nil {
			return Result{}, ErrInvalid
		}
		if c.Record != nil || c.Configuration != nil || c.ExternalReference != nil || c.State != "" {
			return Result{}, ErrInvalid
		}
	case "record":
		if c.Record == nil || c.Record.Validate(c.At) != nil || c.Configuration != nil || c.ExternalReference != nil || c.State != "" || c.VerificationID != "" {
			return Result{}, ErrInvalid
		}
	case "configure":
		if c.Configuration == nil || c.Configuration.Validate() != nil || c.Configuration.Region != s.region || c.Record != nil || c.SubjectID != "" || c.ExternalReference != nil || c.State != "" || c.VerificationID != "" {
			return Result{}, ErrInvalid
		}
	case "delete", "rebuild":
		if c.Record != nil || c.Configuration != nil || c.ExternalReference != nil || c.State != "" || c.VerificationID != "" {
			return Result{}, ErrInvalid
		}
	}
	return s.repository.Execute(ctx, auth.TenantScope(), c)
}

// Read applies tenant read/reveal permissions independently of transport middleware.
func (s *Service) Read(ctx context.Context, auth access.Context, q Query) (Result, error) {
	permission := access.PermissionIdentityRead
	switch q.Kind {
	case "subject", "subjects", "verifications", "external_lookup":
		permission = access.PermissionSubjectsRead
	case "record", "records", "identifier_lookup", "configuration", "receipt":
	default:
		return Result{}, ErrInvalid
	}
	if e := auth.Require(permission); e != nil {
		return Result{}, e
	}
	if q.Reveal && q.Kind != "subject" && q.Kind != "record" && q.Kind != "records" {
		return Result{}, ErrInvalid
	}
	if q.Reveal {
		if e := auth.Require(access.PermissionIdentityReveal); e != nil {
			return Result{}, e
		}
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 {
		return Result{}, ErrInvalid
	}
	if q.SubjectID != "" {
		if _, e := id.ParseSubject(q.SubjectID); e != nil {
			return Result{}, ErrInvalid
		}
	}
	if q.Kind == "subject" || q.Kind == "verifications" || q.Kind == "records" || q.Kind == "record" {
		if q.SubjectID == "" {
			return Result{}, ErrInvalid
		}
	}
	if q.Kind == "record" && !ValidRecordID(q.Reference) {
		return Result{}, ErrInvalid
	}
	if q.RecordKind != "" && q.RecordKind != "observation" && q.RecordKind != "fact" && q.RecordKind != "claim" && q.RecordKind != "identifier" {
		return Result{}, ErrInvalid
	}
	if len(q.After) > 200 {
		return Result{}, ErrInvalid
	}
	if q.Kind == "external_lookup" && (q.ExternalReference == "" || len(q.ExternalReference) > 200) {
		return Result{}, ErrInvalid
	}
	if q.Kind == "identifier_lookup" {
		if q.Identifier == nil || !validName(q.Identifier.Namespace) || !validName(q.Identifier.Issuer) {
			return Result{}, ErrInvalid
		}
		if _, e := Normalize(Value{Type: "string", Text: q.Identifier.Value}, q.Identifier.Normalization); e != nil {
			return Result{}, e
		}
	}
	if q.Kind == "receipt" && !validDigest(q.Reference) {
		return Result{}, ErrInvalid
	}
	q.At = s.now().UTC().Truncate(time.Microsecond)
	q.Region = s.region
	q.Actor = auth.Principal().KeyID()
	return s.repository.Read(ctx, auth.TenantScope(), q)
}
