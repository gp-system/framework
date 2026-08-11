package realtime

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/centrifugal/centrifuge"

	"github.com/gp-system/auth/rbac"
	"github.com/gp-system/errs"
)

func TestRPCRegistry_UnregisteredMethodNotFound(t *testing.T) {
	rpc := newRPCRegistry()
	if _, ok := rpc.lookup("chat.send"); ok {
		t.Fatal("expected an unregistered method to have no handler")
	}
}

func TestRPCRegistry_RegisteredHandlerReceivesIdentityAndData(t *testing.T) {
	rpc := newRPCRegistry()
	var gotID *rbac.Identity
	var gotData []byte
	rpc.Handle("chat.send", func(_ context.Context, id *rbac.Identity, data []byte) ([]byte, error) {
		gotID = id
		gotData = data
		return []byte(`{"ok":true}`), nil
	})

	fn, ok := rpc.lookup("chat.send")
	if !ok {
		t.Fatal("expected a registered handler to be found")
	}
	want := &rbac.Identity{Subject: "u1"}
	out, err := fn(context.Background(), want, []byte(`{"body":"hi"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("out = %q", out)
	}
	if gotID != want {
		t.Fatal("handler did not receive the identity passed in")
	}
	if string(gotData) != `{"body":"hi"}` {
		t.Fatalf("gotData = %q", gotData)
	}
}

func TestRPCError_MapsByStatus(t *testing.T) {
	notFound := errs.Define("test_not_found", errs.Public("no such thing"), errs.Status(http.StatusNotFound))
	badRequest := errs.Define("test_bad_request", errs.Public("bad body"), errs.Status(http.StatusBadRequest))
	forbidden := errs.Define("test_forbidden", errs.Public("nope"), errs.Status(http.StatusForbidden))
	unauthorized := errs.Define("test_unauthorized", errs.Public("who are you"), errs.Status(http.StatusUnauthorized))

	cases := []struct {
		name    string
		err     error
		want    *centrifuge.Error
		message string
	}{
		{"not found", notFound.New("x"), rpcErrorNotFound, "no such thing"},
		{"bad request", badRequest.New("x"), centrifuge.ErrorBadRequest, "bad body"},
		{"unprocessable entity", errs.New("x", errs.Public("invalid"), errs.Status(http.StatusUnprocessableEntity)), centrifuge.ErrorBadRequest, "invalid"},
		{"forbidden", forbidden.New("x"), centrifuge.ErrorPermissionDenied, "nope"},
		{"unauthorized", unauthorized.New("x"), centrifuge.ErrorPermissionDenied, "who are you"},
		{"no status, plain error", errors.New("boom"), centrifuge.ErrorInternal, centrifuge.ErrorInternal.Message},
		{"status with no public message", errs.New("x", errs.Status(http.StatusNotFound)), rpcErrorNotFound, rpcErrorNotFound.Message},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rpcError(tc.err)
			if got.Code != tc.want.Code {
				t.Fatalf("Code = %d, want %d", got.Code, tc.want.Code)
			}
			if got.Message != tc.message {
				t.Fatalf("Message = %q, want %q", got.Message, tc.message)
			}
		})
	}
}
