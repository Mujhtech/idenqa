package experience

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// AssetApprovalSchema identifies the immutable storage-side approval record.
// The public Asset wire shape and portable schema version remain unchanged.
const AssetApprovalSchema = "idenqa.experience-asset-approval.v1"

// AssetApproval is metadata written by a trusted regional processor or a
// self-hosted operator after vetting. Ordinary tenant API credentials cannot
// write the approval namespace. Key aliases in documents do not change approval.
type AssetApproval struct {
	Schema        string `json:"schema"`
	TenantID      string `json:"tenantId"`
	ObjectKey     string `json:"objectKey"`
	ObjectVersion string `json:"objectVersion"`
	MIME          string `json:"mime"`
	Size          int64  `json:"size"`
	Digest        string `json:"digest"`
}

// AssetApprovalReference identifies metadata, not a document image asset.
type AssetApprovalReference struct {
	ObjectKey     string
	ObjectVersion string
	Size          int64
	Digest        string
}

// AssetApprovalObject derives the exact canonical approval bytes and immutable
// metadata reference. Storage readers can verify it without listing objects.
func AssetApprovalObject(tenantID string, asset Asset) (AssetApprovalReference, []byte, error) {
	prefix := "experiences/" + tenantID + "/approved/"
	if tenantID == "" || strings.ContainsAny(tenantID, "/\\\r\n ") || !strings.HasPrefix(asset.ObjectKey, prefix) || !validObjectKey(asset.ObjectKey) || !validObjectVersion(asset.ObjectVersion) || asset.Kind != "image" || asset.Size < 1 || asset.Size > MaxAssetBytes || !digestPattern.MatchString(asset.Digest) {
		return AssetApprovalReference{}, nil, fmt.Errorf("%w: asset approval binding", ErrInvalid)
	}
	if _, ok := allowedMIMEs[asset.MIME]; !ok {
		return AssetApprovalReference{}, nil, fmt.Errorf("%w: asset approval media", ErrInvalid)
	}
	record := AssetApproval{Schema: AssetApprovalSchema, TenantID: tenantID, ObjectKey: asset.ObjectKey, ObjectVersion: asset.ObjectVersion, MIME: asset.MIME, Size: asset.Size, Digest: asset.Digest}
	raw, err := json.Marshal(record)
	if err != nil {
		return AssetApprovalReference{}, nil, err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	return AssetApprovalReference{Size: int64(len(raw)), Digest: digest, ObjectKey: asset.ObjectKey + "/_approval", ObjectVersion: digest}, raw, nil
}
