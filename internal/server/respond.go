package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mohamed-foly/orchdock/internal/domain"
	"github.com/mohamed-foly/orchdock/internal/host"
)

// handler is an API handler that reports failure by returning an error.
type handler func(w http.ResponseWriter, r *http.Request) error

// h turns a handler into an http.HandlerFunc that writes errors as JSON.
func h(fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			fail(w, err)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// errNotImplemented marks routes this service cannot serve (no native shell).
var errNotImplemented = errors.New("not available in this service")

// fail maps domain errors to HTTP statuses and stable error codes.
func fail(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.Is(err, errNotImplemented):
		writeErr(w, http.StatusNotImplemented, "not_implemented", err.Error())
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, host.ErrNoHost):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrStartFailed):
		writeErr(w, http.StatusBadGateway, "start_failed", err.Error())
	case errors.As(err, &tooBig):
		writeErr(w, http.StatusRequestEntityTooLarge, "invalid", "the request body is too large")
	case errors.As(err, &syn), errors.As(err, &typ), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, domain.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// readBody reads the (size-limited) request body.
func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// decode reads a JSON body into v. An empty body leaves v unchanged.
func decode(r *http.Request, v any) error {
	b, err := readBody(r)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(v); err != nil {
		var syn *json.SyntaxError
		var typ *json.UnmarshalTypeError
		if errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: bad JSON: %v", domain.ErrInvalid, err)
		}
		return err
	}
	return nil
}
