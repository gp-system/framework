package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/gp-system/framework/server"
	"github.com/gp-system/httperr"
	"github.com/gp-system/httperr/validate"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
}

// TestRunLifecycle covers the chassis promise end to end: the server starts,
// an in-flight request survives shutdown, closers run LIFO, Run returns nil.
func TestRunLifecycle(t *testing.T) {
	addr := freeAddr(t)
	base := "http://" + addr

	var mu sync.Mutex
	var order []string
	closerFor := func(name string) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			return nil
		}
	}

	cfg := server.Config{ListenAddr: addr, ShutdownTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan error, 1)
	go func() {
		runDone <- server.Run(ctx, cfg, func(r chi.Router) error {
			r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			})
			r.Get("/slow", func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(300 * time.Millisecond)
				_, _ = w.Write([]byte("slow-done"))
			})
			return nil
		},
			server.WithoutTelemetry(),
			server.WithCloser("first", closerFor("first")),
			server.WithCloser("second", closerFor("second")),
		)
	}()

	waitReady(t, base+"/healthz")

	// Start an in-flight request, then trigger shutdown while it runs.
	slowBody := make(chan string, 1)
	slowErr := make(chan error, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			slowErr <- err
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		slowBody <- string(b)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case body := <-slowBody:
		if body != "slow-done" {
			t.Errorf("in-flight response = %q, want slow-done", body)
		}
	case err := <-slowErr:
		t.Errorf("in-flight request failed during graceful shutdown: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never completed")
	}

	select {
	case err := <-runDone:
		if err != nil {
			t.Errorf("Run returned %v, want nil on clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "second" || order[1] != "first" {
		t.Errorf("closer order = %v, want LIFO [second first]", order)
	}
}

func TestRunRegisterErrorPropagates(t *testing.T) {
	cfg := server.Config{ListenAddr: freeAddr(t), ShutdownTimeout: time.Second}
	err := server.Run(context.Background(), cfg, func(chi.Router) error {
		return errors.New("wiring broken")
	}, server.WithoutTelemetry())
	if err == nil {
		t.Fatal("want register error, got nil")
	}
}

func doReq(t *testing.T, router *chi.Mux, method, path string) (*http.Response, httperr.Problem) {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var p httperr.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, body)
	}
	return resp, p
}

func TestNewWritesProblems(t *testing.T) {
	router := server.New(server.Config{}, server.WithoutTelemetry())
	router.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic(fmt.Errorf("kaboom"))
	})
	router.Get("/only-get", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("unknown route yields 404 problem", func(t *testing.T) {
		resp, p := doReq(t, router, "GET", "/nope")
		if resp.StatusCode != 404 || p.Status != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("content-type = %q", ct)
		}
	})

	t.Run("wrong method yields 405 problem", func(t *testing.T) {
		resp, p := doReq(t, router, "POST", "/only-get")
		if resp.StatusCode != 405 || p.Status != 405 {
			t.Errorf("status = %d, want 405", resp.StatusCode)
		}
	})

	t.Run("panic yields 500 problem without detail", func(t *testing.T) {
		resp, p := doReq(t, router, "GET", "/boom")
		if resp.StatusCode != 500 || p.Status != 500 {
			t.Errorf("status = %d, want 500", resp.StatusCode)
		}
		if p.Detail != "" {
			t.Errorf("panic detail leaked to client: %q", p.Detail)
		}
	})

	t.Run("mounted subrouter inherits the 404 problem", func(t *testing.T) {
		sub := chi.NewRouter()
		sub.Get("/here", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		router.Mount("/api/v1", sub)
		resp, p := doReq(t, router, "GET", "/api/v1/nope")
		if resp.StatusCode != 404 || p.Status != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func panicHandlerForTest(http.ResponseWriter, *http.Request) { panic("kaboom") }

func TestPanicStackInDev(t *testing.T) {
	cfg := server.Config{}
	cfg.ExposeInternalErrors = true
	router := server.New(cfg, server.WithoutTelemetry())
	router.Get("/boom", panicHandlerForTest)

	resp, p := doReq(t, router, "GET", "/boom")
	if resp.StatusCode != 500 || p.Status != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if p.Detail != "panic: kaboom" {
		t.Errorf("detail = %q, want the string-panic message in dev", p.Detail)
	}
	if len(p.Stack) == 0 {
		t.Fatal("no stack in dev response")
	}
	var sawSite bool
	for _, f := range p.Stack {
		if strings.HasSuffix(f.Function, "panicHandlerForTest") {
			sawSite = true
		}
	}
	if !sawSite {
		t.Errorf("stack does not point at the panic site: %+v", p.Stack)
	}
}

func TestStrictValidator(t *testing.T) {
	// Mimic an oapi-codegen generated package: per-package handler func type
	// and a request object with validate tags.
	type strictHandlerFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error)
	type createBody struct {
		Title string `json:"title" validate:"required,min=3"`
	}
	type createRequestObject struct {
		Body *createBody
	}

	called := false
	handler := strictHandlerFunc(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		called = true
		return nil, nil
	})
	wrapped := server.StrictValidator[strictHandlerFunc](nil)(handler, "createThing")

	req := httptest.NewRequest("POST", "/things", nil)
	_, err := wrapped(req.Context(), httptest.NewRecorder(), req, createRequestObject{Body: &createBody{Title: "ab"}})
	var p *httperr.Problem
	if !errors.As(err, &p) || p.Status != 422 {
		t.Fatalf("err = %v, want 422 Problem", err)
	}
	if called {
		t.Error("handler ran despite validation failure")
	}

	_, err = wrapped(req.Context(), httptest.NewRecorder(), req, createRequestObject{Body: &createBody{Title: "valid title"}})
	if err != nil || !called {
		t.Errorf("valid request: err = %v, called = %v", err, called)
	}
}

type strictHandlerFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error)

// gateValidator returns a validator whose custom "gate" tag passes only when
// pass is true: a rule the built-in validate.New() does not know, so its
// presence proves which validator actually ran.
func gateValidator(pass bool) *validate.Validator {
	return validate.New(validate.WithRegister(func(v *validator.Validate) {
		_ = v.RegisterValidation("gate", func(validator.FieldLevel) bool { return pass })
	}))
}

// TestWithValidatorFlowsToStrict proves server.WithValidator reaches a
// StrictValidator(nil) middleware through the request context, giving chi
// parity with server.WithValidator without editing generated code.
func TestWithValidatorFlowsToStrict(t *testing.T) {
	type body struct {
		Code string `json:"code" validate:"gate"`
	}
	type reqObj struct{ Body *body }

	handler := strictHandlerFunc(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return nil, nil
	})
	wrapped := server.StrictValidator[strictHandlerFunc](nil)(handler, "createThing")

	// gate=false: the injected validator rejects every request. If the
	// validator did not flow through, StrictValidator would fall back to
	// validate.New(), whose unknown "gate" tag panics into a 500 instead.
	router := server.New(server.Config{}, server.WithoutTelemetry(), server.WithValidator(gateValidator(false)))
	router.Get("/v", func(w http.ResponseWriter, r *http.Request) {
		if _, err := wrapped(r.Context(), w, r, reqObj{Body: &body{Code: "x"}}); err != nil {
			var p *httperr.Problem
			if errors.As(err, &p) {
				w.WriteHeader(p.Status)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/v", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 from the injected validator", rec.Code)
	}
}

// TestStrictValidatorExplicitBeatsContext confirms an explicit validator
// passed to StrictValidator wins over one injected via WithValidator.
func TestStrictValidatorExplicitBeatsContext(t *testing.T) {
	type body struct {
		Code string `json:"code" validate:"gate"`
	}
	type reqObj struct{ Body *body }

	handler := strictHandlerFunc(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return nil, nil
	})
	// Explicit validator rejects; context validator would accept.
	wrapped := server.StrictValidator[strictHandlerFunc](gateValidator(false))(handler, "op")

	router := server.New(server.Config{}, server.WithoutTelemetry(), server.WithValidator(gateValidator(true)))
	router.Get("/v", func(w http.ResponseWriter, r *http.Request) {
		if _, err := wrapped(r.Context(), w, r, reqObj{Body: &body{Code: "x"}}); err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/v", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (explicit validator must win over context)", rec.Code)
	}
}
