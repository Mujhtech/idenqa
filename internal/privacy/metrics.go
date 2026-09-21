package privacy

import "github.com/Mujhtech/idenqa/internal/platform/observability"

// Metrics is the bounded privacy metric receiver owned by this boundary.
type Metrics interface {
	RecordDeletionTransition(observability.DeletionTransition)
	RecordDeletionBacklog(observability.DeletionBacklog)
	RecordBackupExpiry(observability.BackupExpiry)
}

func deletionState(state DeletionState) observability.DeletionState {
	switch state {
	case DeletionRequested:
		return observability.DeletionRequested
	case DeletionBlockedByLegalHold:
		return observability.DeletionHeld
	case DeletionInProgress:
		return observability.DeletionInProgress
	case DeletionAwaitingBackup:
		return observability.DeletionAwaiting
	case DeletionCompleted:
		return observability.DeletionCompleted
	case DeletionFailed:
		return observability.DeletionFailed
	default:
		return observability.DeletionOther
	}
}

// deletionClass collapses a mixed target set into one bounded class label.
func deletionClass(targets []Target) observability.DataClass {
	classes := make(map[observability.DataClass]struct{}, 2)
	for _, target := range targets {
		switch DataClass(target.Kind) {
		case DataClassRawEvidence:
			classes[observability.DataRawEvidence] = struct{}{}
		case DataClassDerivedEvidence:
			classes[observability.DataDerivedEvidence] = struct{}{}
		case DataClassWebhookPayload:
			classes[observability.DataWebhookPayload] = struct{}{}
		case DataClassWorkflowMetadata:
			classes[observability.DataWorkflowMeta] = struct{}{}
		case DataClassAuditRecord:
			classes[observability.DataAuditRecord] = struct{}{}
		case DataClassDeletionProof:
			classes[observability.DataDeletionProof] = struct{}{}
		case DataClassBackup:
			classes[observability.DataBackup] = struct{}{}
		default:
			classes[observability.DataOther] = struct{}{}
		}
	}
	switch len(classes) {
	case 0:
		return observability.DataOther
	case 1:
		for class := range classes {
			return class
		}
	}
	return observability.DataMixed
}
