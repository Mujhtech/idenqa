package verification

import (
	"github.com/Mujhtech/idenqa/internal/pack"
)

// DocumentSupportResolver is the read-only consumer-side port for the active
// pack registry. It exposes classification metadata only and never contributes
// a fact, signal, or assurance meaning.
type DocumentSupportResolver interface {
	Support(country, documentType string) (pack.SupportProjection, bool)
}

// WithDocumentSupport attaches the active pack resolver to deterministic
// document post-processing. When the active pack explicitly marks the
// classified document unsupported, the classification signal becomes
// provisional. No other support level changes signal meaning and no assurance
// semantics change.
func WithDocumentSupport(resolver DocumentSupportResolver) ResultOption {
	return func(options *resultOptions) {
		if resolver != nil {
			options.support = resolver
		}
	}
}
