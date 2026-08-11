package server

import (
	"context"
	"net/http"

	"github.com/gp-system/httperr/validate"
)

// StrictValidator returns an oapi-codegen strict middleware that validates
// the whole request object (parameters and body) with the kit validator
// before the handler runs; failures surface as 422 Problems through the
// generated server's ResponseErrorHandlerFunc.
//
// The type parameter exists because oapi-codegen defines StrictHandlerFunc
// per generated package. The validator is resolved per request in this order:
// the explicit v argument, else the validator set by server.WithValidator
// (found in the request context), else validate.New(). Passing nil is the
// common case and lets a single server.WithValidator option apply
// application-wide without editing generated code:
//
//	gen.NewStrictHandlerWithOptions(handler,
//		[]gen.StrictMiddlewareFunc{server.StrictValidator[gen.StrictHandlerFunc](nil)},
//		gen.StrictHTTPServerOptions{ /* ... */ })
func StrictValidator[H ~func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error)](
	v *validate.Validator,
) func(f H, operationID string) H {
	// Built once so the nil-fallback isn't reconstructed per request.
	fallback := validate.New()
	return func(f H, _ string) H {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if request != nil {
				val := v
				if val == nil {
					val = validatorFromContext(r.Context())
				}
				if val == nil {
					val = fallback
				}
				if err := val.Struct(request); err != nil {
					return nil, err
				}
			}
			return f(ctx, w, r, request)
		}
	}
}
