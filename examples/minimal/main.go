// Command minimal is the smallest possible gpsystem service: one file, one
// route, full chassis (graceful shutdown, problem+json errors, request
// validation and dev-mode logging) all come from server.Run.
//
//	OTEL_DEV_MODE=true go run ./examples/minimal
//	curl localhost:3000/healthz
//	curl -X POST localhost:3000/echo -H 'Content-Type: application/json' -d '{"name":"x"}'
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gp-system/envconf"
	"github.com/gp-system/framework/server"
	"github.com/gp-system/httperr"
	"github.com/gp-system/httperr/validate"
)

type echoRequest struct {
	Name string `json:"name" validate:"required,min=2"`
}

func main() {
	cfg := envconf.MustLoad[server.Config]()
	val := validate.New()

	err := server.Run(context.Background(), cfg, func(r chi.Router) error {
		r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})

		// Decoding and validating by hand here mirrors what the generated
		// oapi-codegen servers do via server.StrictValidator: a too-short name
		// yields a 422 problem+json response.
		r.Post("/echo", func(w http.ResponseWriter, req *http.Request) {
			var body echoRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				httperr.WriteBadRequest(w, req, err)
				return
			}
			if err := val.Struct(body); err != nil {
				httperr.WriteBadRequest(w, req, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"hello": body.Name})
		})

		r.Get("/teapot", func(w http.ResponseWriter, req *http.Request) {
			httperr.WriteError(w, req, httperr.New(http.StatusTeapot, "", "short and stout"))
		})
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
}
