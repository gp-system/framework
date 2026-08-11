package realtime

import (
	"context"
	"net/http"
	"sync"

	"github.com/centrifugal/centrifuge"

	"github.com/gp-system/auth/rbac"
	"github.com/gp-system/errs"
)

// RPCHandler answers one client-initiated RPC call over the connection's
// already-authenticated identity (the same *rbac.Identity a TopicAuth
// callback sees). data is the call's raw request payload; the returned bytes
// become the raw reply payload, both opaque to centrifuge, exactly like an
// HTTP handler's request/response body: JSON-encode/decode is the handler's
// own business, not the gateway's.
//
// An error's mapping to what the client sees mirrors httperr: an
// errs.Definition or *errs.Error's Status (errs.Status) picks the
// centrifuge.Error returned to the client (see rpcError), and its Public
// message (errs.Public) becomes the message text; an error with neither maps
// to ErrorInternal with no detail leaked, same as httperr's unmapped-error
// default.
type RPCHandler func(ctx context.Context, id *rbac.Identity, data []byte) ([]byte, error)

// RPCRegistry holds the project's client-callable RPC methods, registered
// once at startup through WithRPC, before the gateway starts accepting
// connections. A method with no registered handler is rejected with
// centrifuge.ErrorMethodNotFound.
type RPCRegistry struct {
	mu       sync.RWMutex
	handlers map[string]RPCHandler
}

func newRPCRegistry() *RPCRegistry {
	return &RPCRegistry{handlers: map[string]RPCHandler{}}
}

// Handle registers fn as the handler for method. Registering the same
// method twice overwrites the previous handler: this runs once at startup
// under RegisterRPCFunc's control, not at request time.
func (r *RPCRegistry) Handle(method string, fn RPCHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[method] = fn
}

func (r *RPCRegistry) lookup(method string) (RPCHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.handlers[method]
	return fn, ok
}

// rpcErrorNotFound is a custom RPC error for handler errors carrying an HTTP
// 404 Status. centrifuge has no built-in "not found" (ErrorUnknownChannel is
// specific to channel subscribe), so this uses a code in the 400-1999 range
// centrifuge reserves for application-specific errors.
var rpcErrorNotFound = &centrifuge.Error{Code: 404, Message: "not found"}

// rpcError maps a handler's error to the centrifuge.Error a client sees,
// mirroring httperr/map.go's status-to-response mapping: a Status of
// 400/422 becomes ErrorBadRequest, 401/403 ErrorPermissionDenied (a
// connection past connect-time auth failing an RPC is a permission problem,
// not a missing-credentials one), 404 rpcErrorNotFound, everything else
// (including no Status at all, i.e. a plain unwrapped error) ErrorInternal.
func rpcError(err error) *centrifuge.Error {
	switch errs.StatusOf(err) {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return withPublicMessage(centrifuge.ErrorBadRequest, err)
	case http.StatusUnauthorized, http.StatusForbidden:
		return withPublicMessage(centrifuge.ErrorPermissionDenied, err)
	case http.StatusNotFound:
		return withPublicMessage(rpcErrorNotFound, err)
	default:
		return withPublicMessage(centrifuge.ErrorInternal, err)
	}
}

// withPublicMessage returns a copy of base with its Message replaced by
// err's public message, if any; base's own Message is the fallback, so an
// unmapped error never leaks internal detail to the client (mirroring
// httperr's default when Public is unset).
func withPublicMessage(base *centrifuge.Error, err error) *centrifuge.Error {
	msg := errs.PublicOf(err)
	if msg == "" {
		msg = base.Message
	}
	return &centrifuge.Error{Code: base.Code, Message: msg, Temporary: base.Temporary}
}
