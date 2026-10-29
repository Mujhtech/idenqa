package managedlaunch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// PreparationRoute is operator-installed regional configuration. Notice copy
// must already exist in the named tenant; a signed launch cannot invent it.
type PreparationRoute struct {
	TenantID           string `json:"tenantId"`
	ProfileID          string `json:"profileId"`
	PolicyID           string `json:"policyId"`
	NoticeID           string `json:"noticeId"`
	Locale             string `json:"locale"`
	Category           string `json:"category"`
	Purpose            string `json:"purpose"`
	Jurisdiction       string `json:"jurisdiction"`
	PolicyPack         string `json:"policyPack"`
	RecipientReference string `json:"recipientReference"`
	RetentionReference string `json:"retentionReference"`
}

// PreparationConfiguration is a closed, bounded set of approved preview routes.
type PreparationConfiguration struct {
	Routes []PreparationRoute `json:"routes"`
}

// ParsePreparationConfiguration decodes one bounded regional configuration.
func ParsePreparationConfiguration(material []byte) (PreparationConfiguration, error) {
	var configuration PreparationConfiguration
	if len(material) > 64<<10 {
		return configuration, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(material))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return configuration, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return configuration, ErrInvalid
	}
	if len(configuration.Routes) < 1 || len(configuration.Routes) > 64 {
		return configuration, ErrInvalid
	}
	return configuration, nil
}

// LaunchPreparation validates configured disclosures and binds processing
// authority before either capture bearer is returned to a browser.
type LaunchPreparation interface {
	Validate(context.Context, Claims) error
	Prepare(context.Context, Claims, verification.Session) error
}

// NoticeReader is the exact tenant-scoped notice capability consumed here.
type NoticeReader interface {
	FindNotice(context.Context, tenant.Scope, id.Notice) (authority.Notice, error)
}

// AuthorityDeclarer consumes a verified workload for one exact session.
type AuthorityDeclarer interface {
	DeclareManaged(context.Context, tenant.Scope, verification.SessionPrincipal, string, string, authority.DeclarationInput) (authority.Authority, error)
}

// Preparation attaches the approved notice to the real Core workflow.
type Preparation struct {
	routes  map[string]PreparationRoute
	notices NoticeReader
	declare AuthorityDeclarer
	now     func() time.Time
}

// NewPreparation validates and snapshots the operator-installed route set.
func NewPreparation(configuration PreparationConfiguration, notices NoticeReader, declare AuthorityDeclarer, now func() time.Time) (*Preparation, error) {
	if notices == nil || declare == nil || now == nil || len(configuration.Routes) < 1 || len(configuration.Routes) > 64 {
		return nil, ErrInvalid
	}
	preparation := &Preparation{routes: make(map[string]PreparationRoute), notices: notices, declare: declare, now: now}
	for _, route := range configuration.Routes {
		if !validPreparationRoute(route) {
			return nil, ErrInvalid
		}
		key := preparationKey(route.TenantID, route.ProfileID, route.PolicyID, route.Locale)
		if _, exists := preparation.routes[key]; exists {
			return nil, ErrInvalid
		}
		preparation.routes[key] = route
	}
	return preparation, nil
}

// Validate rejects unconfigured routes and cross-tenant or future notices before
// creating a session. It makes no subject response on the subject's behalf.
func (preparation *Preparation) Validate(ctx context.Context, claims Claims) error {
	_, _, err := preparation.selectNotice(ctx, claims)
	return err
}

// Prepare is retry-safe with immutable session times and the original command.
// Consent remains required and is collected directly from the subject by Core.
func (preparation *Preparation) Prepare(ctx context.Context, claims Claims, session verification.Session) error {
	route, notice, err := preparation.selectNotice(ctx, claims)
	if err != nil {
		return err
	}
	if session.TenantID().String() != route.TenantID || session.ProfileID().String() != route.ProfileID ||
		session.PolicyID().String() != route.PolicyID || session.ProfileRevision() != claims.ProfileRevision ||
		session.Region() != claims.Scope.Region || session.CreatedAt().Before(notice.EffectiveAt()) {
		return ErrForbidden
	}
	scope, err := tenant.NewScope(session.TenantID())
	if err != nil {
		return ErrInvalid
	}
	principal, err := verification.NewWorkloadSessionPrincipal(claims.WorkloadID)
	if err != nil {
		return ErrInvalid
	}
	_, err = preparation.declare.DeclareManaged(ctx, scope, principal, claims.ActorID, claims.CommandID+".authority", authority.DeclarationInput{
		VerificationID: session.ID(), NoticeID: notice.ID(), Category: route.Category,
		Purpose: route.Purpose, Jurisdiction: route.Jurisdiction, PolicyPack: route.PolicyPack,
		IsConsentRequired: true, RecipientReference: route.RecipientReference,
		RecipientDisplayName: notice.Recipient(), Regions: []string{session.Region()},
		RetentionReference: route.RetentionReference, ValidFrom: session.CreatedAt(), ExpiresAt: session.ExpiresAt(),
	})
	if err != nil {
		return fmt.Errorf("prepare managed launch processing authority: %w", err)
	}
	return nil
}

func (preparation *Preparation) selectNotice(ctx context.Context, claims Claims) (PreparationRoute, authority.Notice, error) {
	route, ok := preparation.routes[preparationKey(claims.CoreTenantID, claims.ProfileID, claims.PolicyID, claims.Locale)]
	if !ok {
		return PreparationRoute{}, authority.Notice{}, ErrForbidden
	}
	tenantID, _ := id.ParseTenant(route.TenantID)
	scope, _ := tenant.NewScope(tenantID)
	noticeID, _ := id.ParseNotice(route.NoticeID)
	notice, err := preparation.notices.FindNotice(ctx, scope, noticeID)
	if err != nil || notice.ID() != noticeID || notice.TenantID() != tenantID || notice.Locale() != route.Locale || notice.EffectiveAt().After(preparation.now()) {
		return PreparationRoute{}, authority.Notice{}, ErrForbidden
	}
	return route, notice, nil
}

func preparationKey(tenantID, profileID, policyID, locale string) string {
	return tenantID + ":" + profileID + ":" + policyID + ":" + locale
}

func validPreparationRoute(route PreparationRoute) bool {
	_, tenantErr := id.ParseTenant(route.TenantID)
	_, profileErr := id.ParseProfile(route.ProfileID)
	_, policyErr := id.ParsePolicy(route.PolicyID)
	_, noticeErr := id.ParseNotice(route.NoticeID)
	if tenantErr != nil || profileErr != nil || policyErr != nil || noticeErr != nil || !bounded(route.Locale, 35) {
		return false
	}
	for _, value := range []string{route.Category, route.Purpose, route.Jurisdiction, route.PolicyPack, route.RecipientReference, route.RetentionReference} {
		if !bounded(value, 200) {
			return false
		}
	}
	return true
}
