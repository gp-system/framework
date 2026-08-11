package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gp-system/auth"
	"github.com/gp-system/auth/policy"
	"github.com/gp-system/auth/rbac"
	"github.com/gp-system/framework/server"
	"github.com/gp-system/httperr"
)

var authCfg = auth.Config{
	Secret:    "test-secret-please-rotate",
	AccessTTL: time.Minute,
	Issuer:    "gpsystem-test",
}

func doAuthHTTP(t *testing.T, handler http.Handler, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/protected", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) httperr.Problem {
	t.Helper()
	var p httperr.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return p
}

func TestAuthMiddleware(t *testing.T) {
	var seen *rbac.Identity
	handler := server.AuthMiddleware(authCfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = rbac.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("missing token", func(t *testing.T) {
		rec := doAuthHTTP(t, handler, "")
		if rec.Code != 401 {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("content-type = %q", ct)
		}
		if p := problemOf(t, rec); p.Detail != "missing bearer token" {
			t.Errorf("unexpected problem: %+v", p)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		rec := doAuthHTTP(t, handler, "Bearer not-a-jwt")
		if rec.Code != 401 {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if p := problemOf(t, rec); p.Detail != "invalid or expired token" {
			t.Errorf("unexpected problem: %+v", p)
		}
	})

	t.Run("valid token reaches handler with identity", func(t *testing.T) {
		issuer := auth.NewTokenIssuer[auth.DefaultClaims](authCfg)
		token, err := issuer.Sign(auth.NewDefaultClaims(authCfg, "user-1", "peter", []string{"admin"}, []string{"write:news"}))
		if err != nil {
			t.Fatal(err)
		}
		rec := doAuthHTTP(t, handler, "Bearer "+token)
		if rec.Code != 204 {
			t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
		}
		if seen == nil || seen.Subject != "user-1" || !seen.HasRole("admin") {
			t.Errorf("identity in context = %+v", seen)
		}
	})
}

// TestAuthMiddlewareWithRequireRole walks the full request path: bearer
// token -> AuthMiddleware -> identity in context -> RequireRole -> handler.
func TestAuthMiddlewareWithRequireRole(t *testing.T) {
	handler := server.AuthMiddleware(authCfg)(server.RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := rbac.FromContext(r.Context())
		_, _ = w.Write([]byte("hello " + id.Username))
	})))

	issuer := auth.NewTokenIssuer[auth.DefaultClaims](authCfg)
	adminToken, _ := issuer.Sign(auth.NewDefaultClaims(authCfg, "u1", "peter", []string{"admin"}, nil))
	userToken, _ := issuer.Sign(auth.NewDefaultClaims(authCfg, "u2", "bob", []string{"user"}, nil))

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no token", "", 401},
		{"garbage", "Bearer garbage", 401},
		{"wrong role", "Bearer " + userToken, 403},
		{"admin", "Bearer " + adminToken, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAuthHTTP(t, handler, tc.header)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestRequireRole(t *testing.T) {
	handler := server.RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	do := func(id *rbac.Identity) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/admin", nil)
		if id != nil {
			req = req.WithContext(rbac.WithIdentity(req.Context(), id))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	t.Run("no identity", func(t *testing.T) {
		rec := do(nil)
		if rec.Code != 401 {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("content-type = %q", ct)
		}
	})

	t.Run("wrong role", func(t *testing.T) {
		if rec := do(&rbac.Identity{Roles: []string{"viewer"}}); rec.Code != 403 {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("matching role", func(t *testing.T) {
		if rec := do(&rbac.Identity{Roles: []string{"admin"}}); rec.Code != 204 {
			t.Errorf("status = %d, want 204", rec.Code)
		}
	})
}

func TestRequirePermission(t *testing.T) {
	handler := server.RequirePermission("write:news")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("GET", "/news", nil)
	req = req.WithContext(rbac.WithIdentity(req.Context(), &rbac.Identity{Permissions: []string{"read:news"}}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}

	req = req.WithContext(rbac.WithIdentity(req.Context(), &rbac.Identity{Permissions: []string{"write:news"}}))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestWriteError(t *testing.T) {
	do := func(err error) (*httptest.ResponseRecorder, httperr.Problem) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		server.WriteError(rec, req, err)
		return rec, problemOf(t, rec)
	}

	t.Run("policy deny with detail", func(t *testing.T) {
		rec, p := do(policy.Deny("not the owner"))
		if rec.Code != 403 || p.Detail != "not the owner" {
			t.Errorf("code = %d, problem = %+v", rec.Code, p)
		}
	})

	t.Run("rbac forbidden", func(t *testing.T) {
		rec, p := do(rbac.ErrForbidden)
		if rec.Code != 403 || p.Detail != "insufficient privileges" {
			t.Errorf("code = %d, problem = %+v", rec.Code, p)
		}
	})

	t.Run("rbac unauthenticated", func(t *testing.T) {
		rec, p := do(rbac.ErrUnauthenticated)
		if rec.Code != 401 || p.Detail != "authentication required" {
			t.Errorf("code = %d, problem = %+v", rec.Code, p)
		}
	})

	t.Run("wrapped sentinel still maps", func(t *testing.T) {
		rec, p := do(fmt.Errorf("ctx: %w", rbac.ErrForbidden))
		if rec.Code != 403 || p.Detail != "insufficient privileges" {
			t.Errorf("code = %d, problem = %+v", rec.Code, p)
		}
	})

	t.Run("plain error maps to generic 500", func(t *testing.T) {
		rec, p := do(errors.New("boom"))
		if rec.Code != 500 || p.Detail != "" {
			t.Errorf("code = %d, problem = %+v", rec.Code, p)
		}
	})

	t.Run("existing Problem passes through untouched", func(t *testing.T) {
		rec, p := do(httperr.NotFound("gone"))
		if rec.Code != 404 || p.Detail != "gone" {
			t.Errorf("code = %d, problem = %+v", rec.Code, p)
		}
	})
}

func TestIdentityMiddleware(t *testing.T) {
	var seen *rbac.Identity
	handler := server.IdentityMiddleware(authCfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = rbac.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("missing token passes through anonymous", func(t *testing.T) {
		seen = nil
		rec := doAuthHTTP(t, handler, "")
		if rec.Code != 204 {
			t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
		}
		if seen != nil {
			t.Errorf("identity = %+v, want nil (anonymous)", seen)
		}
	})

	t.Run("invalid token is rejected", func(t *testing.T) {
		rec := doAuthHTTP(t, handler, "Bearer not-a-jwt")
		if rec.Code != 401 {
			t.Errorf("status = %d, want 401", rec.Code)
		}
		if p := problemOf(t, rec); p.Detail != "invalid or expired token" {
			t.Errorf("unexpected problem: %+v", p)
		}
	})

	t.Run("valid token yields identity", func(t *testing.T) {
		seen = nil
		issuer := auth.NewTokenIssuer[auth.DefaultClaims](authCfg)
		token, err := issuer.Sign(auth.NewDefaultClaims(authCfg, "user-1", "peter", []string{"admin"}, []string{"write:news"}))
		if err != nil {
			t.Fatal(err)
		}
		rec := doAuthHTTP(t, handler, "Bearer "+token)
		if rec.Code != 204 {
			t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
		}
		if seen == nil || seen.Subject != "user-1" || !seen.HasPermission("write:news") {
			t.Errorf("identity in context = %+v", seen)
		}
	})
}

// TestIdentityMiddlewareWithRequirePermission pins the generated wiring's
// promise: anonymous requests reach public routes, while a permission guard
// behind the same identity middleware still yields 401 for them.
func TestIdentityMiddlewareWithRequirePermission(t *testing.T) {
	handler := server.IdentityMiddleware(authCfg)(server.RequirePermission("write:news")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	rec := doAuthHTTP(t, handler, "")
	if rec.Code != 401 {
		t.Errorf("anonymous against guard: status = %d, want 401", rec.Code)
	}

	issuer := auth.NewTokenIssuer[auth.DefaultClaims](authCfg)
	token, err := issuer.Sign(auth.NewDefaultClaims(authCfg, "user-1", "peter", nil, []string{"write:news"}))
	if err != nil {
		t.Fatal(err)
	}
	rec = doAuthHTTP(t, handler, "Bearer "+token)
	if rec.Code != 204 {
		t.Errorf("authorized: status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
}
