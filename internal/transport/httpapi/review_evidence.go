package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // Register JPEG decoding for the controlled raster display.
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/go-chi/chi/v5"
)

// ReviewEvidenceRoutes releases only authenticated, redacted, watermarked raster output.
type ReviewEvidenceRoutes struct {
	base    *ReviewRoutes
	service *review.EvidenceService
}

// NewReviewEvidenceRoutes constructs controlled review display routes.
func NewReviewEvidenceRoutes(auth *AccessMiddleware, service *review.EvidenceService, logger *slog.Logger) (*ReviewEvidenceRoutes, error) {
	if auth == nil || service == nil || logger == nil {
		return nil, review.ErrInvalid
	}
	return &ReviewEvidenceRoutes{
		base:    &ReviewRoutes{handlerBase: newHandlerBase(logger, "review"), access: auth},
		service: service,
	}, nil
}

// Register mounts authenticated review endpoints.
func (r *ReviewEvidenceRoutes) Register(router chi.Router) {
	middleware := []func(http.Handler) http.Handler{r.base.access.Authorize(access.PermissionReviewsWrite)}
	router.With(middleware...).Get("/review-cases/{caseID}/evidence", r.list)
	router.With(middleware...).Post("/review-cases/{caseID}/evidence-grants", r.issue)
	router.With(middleware...).Post("/review-evidence-grants/{grantID}/content", r.read)
}
func (r *ReviewEvidenceRoutes) issue(w http.ResponseWriter, req *http.Request) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	caseID, err := r.base.caseID(req)
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[struct {
		Version    int64  `json:"expected_version"`
		EvidenceID string `json:"evidence_id"`
	}](req)
	if err != nil {
		r.base.problem(w, req, invalidRequest(err))
		return
	}
	evidenceID, err := id.ParseEvidence(body.EvidenceID)
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.Issue(req.Context(), auth, caseID, body.Version, evidenceID, key)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusCreated, struct {
		GrantID    string    `json:"grant_id"`
		CaseID     string    `json:"case_id"`
		Version    int64     `json:"case_version"`
		EvidenceID string    `json:"evidence_id"`
		ExpiresAt  time.Time `json:"expires_at"`
	}{result.GrantID.String(), result.CaseID.String(), result.CaseVersion, result.EvidenceID.String(), result.ExpiresAt})
}
func (r *ReviewEvidenceRoutes) read(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Referrer-Policy", "no-referrer")
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	grant, err := id.ParseGrant(chi.URLParam(req, "grantID"))
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	buffer := &reviewDisplayBuffer{}
	defer func() { clear(buffer.output.Bytes()); buffer.output.Reset() }()
	if err := r.service.Read(ctx, auth, grant, buffer); err != nil || buffer.output.Len() == 0 {
		r.base.problem(w, req, review.ErrForbidden)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", "inline; filename=review.png")
	if _, err := w.Write(buffer.output.Bytes()); err != nil {
		r.base.logger.ErrorContext(ctx, "write review evidence")
	}
}

type reviewDisplayBuffer struct {
	access review.EvidenceAccess
	output bytes.Buffer
}

func (b *reviewDisplayBuffer) Configure(value review.EvidenceAccess) { b.access = value }
func (b *reviewDisplayBuffer) Binding() evidence.ReceiverBinding {
	return evidence.ReceiverBinding{Runner: evidence.Runner{Identity: b.access.ReviewerID, WorkloadVersion: "review.display.v1"}, CheckReference: "review." + strings.ToLower(b.access.CaseID.String()), OutputDestination: "review.display"}
}
func (b *reviewDisplayBuffer) Receive(ctx context.Context, _ id.Redemption, mediaType string, produce func(io.Writer) error, afterCommit func() error) error {
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		return evidence.ErrReadDenied
	}
	raw := &reviewLimitedBuffer{}
	defer func() { clear(raw.Bytes()); raw.Reset() }()
	if err := produce(raw); err != nil {
		return err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw.Bytes()))
	if err != nil || (format != "jpeg" && format != "png") || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 12000000 {
		return evidence.ErrReadDenied
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw.Bytes()))
	if err != nil {
		return evidence.ErrReadDenied
	}
	defer clearReviewImage(decoded)
	if err := ctx.Err(); err != nil {
		return err
	}
	canvas := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	defer clear(canvas.Pix)
	draw.Draw(canvas, canvas.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	for _, mask := range b.access.Redactions {
		rectangle := image.Rect(mask.X*config.Width/10000, mask.Y*config.Height/10000, ((mask.X+mask.Width)*config.Width+9999)/10000, ((mask.Y+mask.Height)*config.Height+9999)/10000)
		draw.Draw(canvas, rectangle, image.NewUniform(color.Black), image.Point{}, draw.Src)
	}
	sum := sha256.Sum256([]byte(b.access.ReviewerID + "/" + b.access.CaseID.String() + "/" + b.access.GrantID.String()))
	label := hex.EncodeToString(sum[:8])
	for y := 12; y < config.Height; y += 100 {
		for x := 8; x < config.Width; x += 230 {
			drawReviewWatermark(canvas, x, y, label)
		}
	}
	if err := png.Encode(&b.output, canvas); err != nil {
		return err
	}
	return afterCommit()
}

type reviewLimitedBuffer struct{ bytes.Buffer }

func (b *reviewLimitedBuffer) Write(value []byte) (int, error) {
	if b.Len()+len(value) > 10<<20 {
		return 0, evidence.ErrReadDenied
	}
	return b.Buffer.Write(value)
}

// A small fixed hexadecimal font avoids dynamic font or external rendering dependencies.
func drawReviewWatermark(dst *image.RGBA, x, y int, label string) {
	glyphs := [16]string{"111101101101111", "010110010010111", "111001111100111", "111001111001111", "101101111001001", "111100111001111", "111100111101111", "111001001001001", "111101111101111", "111101111001111", "010101111101101", "110101110101110", "111100100100111", "110101101101110", "111100110100111", "111100110100100"}
	for index, r := range label {
		n := strings.IndexRune("0123456789abcdef", r)
		if n < 0 {
			continue
		}
		for i, pixel := range glyphs[n] {
			if pixel != '1' {
				continue
			}
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					px, py := x+index*8+(i%3)*2+dx, y+(i/3)*2+dy
					dst.Set(px+1, py+1, color.RGBA{0, 0, 0, 255})
					dst.Set(px, py, color.RGBA{255, 255, 255, 255})
				}
			}
		}
	}
}

func (r *ReviewEvidenceRoutes) list(w http.ResponseWriter, req *http.Request) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	identifier, err := r.base.caseID(req)
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	version, err := strconv.ParseInt(req.URL.Query().Get("expected_version"), 10, 64)
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	items, err := r.service.List(req.Context(), auth, identifier, version)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, struct {
		Items []review.EvidenceMetadata `json:"items"`
	}{items})
}

func clearReviewImage(value image.Image) {
	switch pixels := value.(type) {
	case *image.RGBA:
		clear(pixels.Pix)
	case *image.NRGBA:
		clear(pixels.Pix)
	case *image.RGBA64:
		clear(pixels.Pix)
	case *image.NRGBA64:
		clear(pixels.Pix)
	case *image.Gray:
		clear(pixels.Pix)
	case *image.Gray16:
		clear(pixels.Pix)
	case *image.Paletted:
		clear(pixels.Pix)
		clear(pixels.Palette)
	case *image.YCbCr:
		clear(pixels.Y)
		clear(pixels.Cb)
		clear(pixels.Cr)
	}
}
