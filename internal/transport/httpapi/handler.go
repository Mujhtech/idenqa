package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
)

// handlerBase carries the problem and response plumbing shared by route
// registrars. Registrar types embed it so failures are logged and rendered
// uniformly, and so no route repeats the same helper method.
type handlerBase struct {
	logger *slog.Logger
	name   string
}

// newHandlerBase constructs shared route plumbing for a named capability.
func newHandlerBase(logger *slog.Logger, name string) handlerBase {
	return handlerBase{logger: logger, name: name}
}

// problem writes the safe problem-details response for err.
func (base handlerBase) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		base.logger.ErrorContext(request.Context(), "write "+base.name+" problem response")
	}
}

// writeJSON writes a JSON response with the given status.
func (base handlerBase) writeJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	if err := respond.JSON(writer, request, status, value); err != nil {
		base.logger.ErrorContext(request.Context(), "write "+base.name+" response")
	}
}

// reply writes either the problem for err or a no-store JSON response.
func (base handlerBase) reply(writer http.ResponseWriter, request *http.Request, value any, err error) {
	writer.Header().Set("Cache-Control", "no-store")
	if err != nil {
		base.problem(writer, request, err)

		return
	}
	base.writeJSON(writer, request, http.StatusOK, value)
}
