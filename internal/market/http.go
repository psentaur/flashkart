package market

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func serveAPI(mux *http.ServeMux, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Correlation-ID") == "" {
			r.Header.Set("X-Correlation-ID", rand.Text())
		}
		w.Header().Set("X-Correlation-ID", r.Header.Get("X-Correlation-ID"))
		w.Header().Set("Cache-Control", "no-store")
		_, pattern := mux.Handler(r)
		if pattern == "" {
			fallback.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func admin(token string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, provided, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || provided == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, 401, "UNAUTHENTICATED", "administrator bearer token required")
			return
		}
		if !matchesToken(token, provided) {
			writeError(w, r, 403, "FORBIDDEN", "administrator access required")
			return
		}
		handler(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	reader := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	reader.DisallowUnknownFields()
	if err := reader.Decode(value); err != nil || reader.Decode(new(any)) != io.EOF {
		writeError(w, r, 400, "INVALID_ARGUMENT", "invalid JSON request")
		return false
	}
	return true
}

func send(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, httpStatus int, code, message string) {
	send(w, httpStatus, map[string]string{"code": code, "message": message, "correlation_id": r.Header.Get("X-Correlation-ID")})
}

func serviceError(w http.ResponseWriter, r *http.Request, err error) {
	s := status.Convert(err)
	switch s.Code() {
	case codes.InvalidArgument:
		writeError(w, r, 400, "INVALID_ARGUMENT", s.Message())
	case codes.AlreadyExists:
		writeError(w, r, 409, "IDEMPOTENCY_CONFLICT", s.Message())
	case codes.Unavailable, codes.DeadlineExceeded:
		reason := "UNAVAILABLE"
		for _, detail := range s.Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Domain == "flashkart.replication" {
				reason = info.Reason
			}
		}
		writeError(w, r, 503, reason, "replica operation could not be confirmed")
	default:
		writeError(w, r, 500, "INTERNAL", "operation failed")
	}
}

func matchesToken(expected, provided string) bool {
	a, b := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(provided))
	return expected != "" && subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
