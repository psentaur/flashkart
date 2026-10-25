// Package catalog preserves Catalog's display metadata and conflict semantics.
package catalog

import (
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func Query(q url.Values) bool {
	for k, v := range q {
		if len(v) != 1 {
			return false
		}
		switch k {
		case "limit":
			n, e := strconv.Atoi(v[0])
			if e != nil || n < 1 || n > 100 {
				return false
			}
		case "cursor":
			if v[0] == "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}
func Pointer(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }

// Proxy validates upstream responses against the same route schemas as clients.
// A missing product and an unavailable quorum must retain different meanings.
func Proxy(w http.ResponseWriter, r *http.Request, client *http.Client, base, path, contractPath string) {
	if client == nil {
		client = http.DefaultClient
	}
	upstream, e := httpapi.ForwardRequest(r.Context(), r.Method, strings.TrimRight(base, "/")+path, r.Body, r)
	if e != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Upstream unavailable.", nil)
		return
	}
	authHeader := upstream.Header.Get("Authorization")
	upstream.Header = make(http.Header)
	for _, h := range []string{"Content-Type", "X-Correlation-ID", "X-Request-Deadline"} {
		upstream.Header.Set(h, r.Header.Get(h))
	}
	upstream.Header.Set("X-Correlation-ID", httpapi.Correlation(r.Context()))
	if d, ok := r.Context().Deadline(); ok {
		upstream.Header.Set("X-Request-Deadline", d.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	if strings.HasPrefix(contractPath, "/v1/admin/") {
		upstream.Header.Set("Authorization", authHeader)
	}
	response, e := client.Do(upstream)
	if e != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Upstream unavailable.", nil)
		return
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Invalid upstream response.", nil)
		return
	}
	fragment := "/paths/" + Pointer(contractPath) + "/" + strings.ToLower(r.Method) + "/responses/" + strconv.Itoa(response.StatusCode) + "/content/application~1json/schema"
	schema, e := value.Schema("public.openapi.json", fragment, false)
	doc, parse := value.JSON(raw)
	if e != nil || parse != nil || schema.Validate(doc) != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Invalid upstream response.", nil)
		return
	}
	if retry := response.Header.Get("Retry-After"); retry != "" {
		if n, e := strconv.Atoi(retry); e == nil && n > 0 {
			w.Header().Set("Retry-After", retry)
		}
	}
	if boundary := response.Header.Get("X-Sale-Freeze-At"); boundary != "" && value.UTC(boundary) == nil {
		w.Header().Set("X-Sale-Freeze-At", boundary)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(raw)
}
func (c Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := "/v1/products"
	contractPath := path
	if id := r.PathValue("product_id"); id != "" {
		if value.UUID(id) != nil || len(r.URL.Query()) > 0 {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid product request.", nil)
			return
		}
		path += "/" + strings.ToLower(id)
		contractPath += "/{product_id}"
	} else if !Query(r.URL.Query()) {
		httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid pagination.", nil)
		return
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.Query().Encode()
	}
	Proxy(w, r, c.HTTP, c.BaseURL, path, contractPath)
}
