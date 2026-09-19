package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
)

const (
	tenantExportMediaType       = "application/x-ndjson"
	tenantExportMaximumDuration = 30 * time.Minute
	tenantExportMaximumBytes    = int64(8 << 30)
	tenantExportWriteWindow     = 30 * time.Second
)

// TenantExporter is the application capability consumed by the export route.
type TenantExporter interface {
	Export(context.Context, tenantexport.Authority, []tenantexport.Collection, func([]byte) error) error
}

// isTenantExportRequest identifies the streaming export so the ordinary request
// deadline does not truncate it; the handler owns its own duration and write
// window guards.
func isTenantExportRequest(request *http.Request) bool {
	return request.Method == http.MethodGet && request.URL.Path == VersionPrefix+"/tenant/export"
}

// export streams the portable tenant-owned export as canonical NDJSON. Records
// are emitted as they are read, so neither the API nor the client buffers the
// whole export.
func (routes *TenantRoutes) export(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)

		return
	}
	selection, err := tenantExportSelection(request)
	if err != nil {
		routes.writeExportFailure(writer, request, invalidRequest(err))

		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), tenantExportMaximumDuration)
	defer cancel()
	controller := http.NewResponseController(writer)
	writer.Header().Set("Content-Type", tenantExportMediaType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Disposition", `attachment; filename="idenqa-tenant-export.ndjson"`)
	started := false
	written := int64(0)
	emit := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if written+int64(len(line)) > tenantExportMaximumBytes {
			return tenantexport.ErrLimit
		}
		if !started {
			writer.WriteHeader(http.StatusOK)
			started = true
		}
		if err := controller.SetWriteDeadline(time.Now().Add(tenantExportWriteWindow)); err != nil &&
			!errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("set tenant export write deadline: %w", err)
		}
		count, err := writer.Write(line)
		written += int64(count)
		if err != nil {
			return fmt.Errorf("write tenant export line: %w", err)
		}
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}

		return nil
	}
	if err := routes.exporter.Export(ctx, authority, selection, emit); err != nil {
		if !started {
			routes.writeExportFailure(writer, request, err)

			return
		}
		routes.logger.WarnContext(request.Context(), "tenant export stream stopped", "error", err)
	}
}

func (routes *TenantRoutes) writeExportFailure(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(
		writer,
		request,
		err,
		requestIDString(request.Context()),
	); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write tenant export failure response")
	}
}

func tenantExportSelection(request *http.Request) ([]tenantexport.Collection, error) {
	query := request.URL.Query()
	for name, values := range query {
		if name != "collections" {
			return nil, fmt.Errorf("unknown query parameter %q", name)
		}
		if len(values) != 1 {
			return nil, fmt.Errorf("query parameter %q must appear once", name)
		}
	}
	selection, err := tenantexport.ParseCollections(query.Get("collections"))
	if err != nil {
		return nil, errors.New("collections must be a bounded list of known export collections")
	}

	return selection, nil
}

var _ TenantExporter = (*tenantexport.Exporter)(nil)
