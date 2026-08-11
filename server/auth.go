package server

import (
	"errors"
	"net/http"

	"github.com/golang-jwt/jwt/v5"

	"github.com/gp-system/auth"
	"github.com/gp-system/auth/policy"
	"github.com/gp-system/auth/rbac"
	"github.com/gp-system/httperr"
)

// AuthMiddleware is a net/http middleware wrapping auth.Identify with
// auth.DefaultClaims: it extracts the bearer token, verifies it, and puts
// an rbac.Identity in the request context. Failures are rendered as RFC
// 9457 Problems by WriteError.
func AuthMiddleware(cfg auth.Config) func(http.Handler) http.Handler {
	issuer := auth.NewTokenIssuer[auth.DefaultClaims](cfg)
	return AuthMiddlewareFor(issuer, auth.DefaultIdentity)
}

// AuthMiddlewareFor is AuthMiddleware for custom claims types: toIdentity
// maps the parsed claims onto the rbac.Identity used by authorization
// checks.
func AuthMiddlewareFor[C jwt.Claims](issuer *auth.TokenIssuer[C], toIdentity func(C) (*rbac.Identity, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := auth.Identify(issuer, toIdentity, r.Header.Get("Authorization"))
			if err != nil {
				WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(rbac.WithIdentity(r.Context(), id)))
		})
	}
}

// IdentityMiddleware identifies the request WHEN a bearer token is present
// and stores the rbac.Identity in the context; a request without a token
// passes through anonymous. A present but invalid or expired token is still
// rejected with a 401 Problem: silently degrading a bad token to anonymous
// would turn every expired session into a confusing 401/403 further down.
//
// This is the middleware `gpsystem add auth` wires on the api router: it
// keeps public operations (login, register) working while the per-operation
// policy Enforcer supplies the 401 for @permission/@policy-tagged
// operations. Use AuthMiddleware instead for route groups that are private
// in their entirety.
func IdentityMiddleware(cfg auth.Config) func(http.Handler) http.Handler {
	issuer := auth.NewTokenIssuer[auth.DefaultClaims](cfg)
	return IdentityMiddlewareFor(issuer, auth.DefaultIdentity)
}

// IdentityMiddlewareFor is IdentityMiddleware for custom claims types:
// toIdentity maps the parsed claims onto the rbac.Identity used by
// authorization checks.
func IdentityMiddlewareFor[C jwt.Claims](issuer *auth.TokenIssuer[C], toIdentity func(C) (*rbac.Identity, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := auth.Identify(issuer, toIdentity, r.Header.Get("Authorization"))
			switch {
			case errors.Is(err, auth.ErrMissingToken):
				next.ServeHTTP(w, r)
			case err != nil:
				WriteError(w, r, err)
			default:
				next.ServeHTTP(w, r.WithContext(rbac.WithIdentity(r.Context(), id)))
			}
		})
	}
}

// RequireRole is a net/http middleware for chi and other net/http routers.
// Responds 401 without an identity and 403 without the role, rendered as
// RFC 9457 Problems.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return requireFn(func(id *rbac.Identity) error { return rbac.CheckRole(id, roles...) })
}

// RequirePermission is RequireRole for permissions (any-of semantics).
func RequirePermission(perms ...string) func(http.Handler) http.Handler {
	return requireFn(func(id *rbac.Identity) error { return rbac.CheckPermission(id, perms...) })
}

func requireFn(check func(*rbac.Identity) error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := check(rbac.FromContext(r.Context())); err != nil {
				WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteError renders err as an RFC 9457 Problem. It maps the auth module's
// access failures (auth.ErrMissingToken/ErrInvalidToken/ErrInvalidClaims,
// rbac.ErrUnauthenticated/ErrForbidden and policy.Denial, produced by
// AuthMiddleware, RequireRole/RequirePermission, policy.Deny and the policy
// Enforcer) onto Problems, then delegates everything else to
// httperr.WriteError. Its signature matches oapi-codegen's
// ResponseErrorHandlerFunc, so it is the writer generated register
// functions wire for strict handlers.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	httperr.WriteError(w, r, mapAuthError(err))
}

func mapAuthError(err error) error {
	var d *policy.Denial
	switch {
	case errors.As(err, &d):
		return httperr.Forbidden(d.Detail)
	case errors.Is(err, rbac.ErrForbidden):
		return httperr.Forbidden("insufficient privileges")
	case errors.Is(err, rbac.ErrUnauthenticated):
		return httperr.Unauthorized("authentication required")
	case errors.Is(err, auth.ErrMissingToken):
		return httperr.Unauthorized("missing bearer token")
	case errors.Is(err, auth.ErrInvalidToken):
		return httperr.Unauthorized("invalid or expired token")
	case errors.Is(err, auth.ErrInvalidClaims):
		return httperr.Unauthorized("invalid token claims")
	}
	return err
}
