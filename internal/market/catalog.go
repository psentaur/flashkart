package market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
	"google.golang.org/protobuf/proto"
)

const productKind = pb.DocumentKind_DOCUMENT_KIND_PRODUCT
const saleKind = pb.DocumentKind_DOCUMENT_KIND_PUBLISHED_SALE

type Product struct {
	ID          string `json:"product_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

type interval struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

type causalContext map[string][]interval

func contextJSON(value *pb.Context) causalContext {
	result := causalContext{}
	for id, counters := range value.GetObserved() {
		for _, span := range counters.Intervals {
			result[id] = append(result[id], interval{strconv.FormatUint(span.First, 10), strconv.FormatUint(span.Last, 10)})
		}
	}
	return result
}

func (value causalContext) protobuf() (*pb.Context, error) {
	result := &pb.Context{Observed: map[string]*pb.Counters{}}
	for id, spans := range value {
		if len(id) != 1 || !strings.Contains(replicaOrder, id) || len(spans) == 0 {
			return nil, errors.New("invalid causal context")
		}
		counters := &pb.Counters{}
		var previous uint64
		for _, span := range spans {
			first, err1 := strconv.ParseUint(span.First, 10, 63)
			last, err2 := strconv.ParseUint(span.Last, 10, 63)
			if err1 != nil || err2 != nil || first <= previous || last < first {
				return nil, errors.New("invalid causal interval")
			}
			previous = last
			counters.Intervals = append(counters.Intervals, &pb.Interval{First: first, Last: last})
		}
		result.Observed[id] = counters
	}
	return result, nil
}

type Catalog struct {
	Replicas *replica.Client
	Images   *Images
}

func (c Catalog) Handler(token string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/products", c.list(productKind))
	mux.HandleFunc("GET /api/products/{product_id}", c.get(productKind, "product_id"))
	mux.HandleFunc("GET /api/sales", c.list(saleKind))
	mux.HandleFunc("GET /api/sales/{sale_id}", c.get(saleKind, "sale_id"))
	mux.HandleFunc("GET /api/admin/products/{product_id}", admin(token, c.editView))
	mux.HandleFunc("PUT /api/admin/products/{product_id}", admin(token, c.write(false)))
	mux.HandleFunc("POST /api/admin/products/{product_id}/resolve", admin(token, c.write(true)))
	mux.HandleFunc("GET /api/admin/replicas", admin(token, c.inspect))
	mux.HandleFunc("GET /api/admin/replicas/products/{product_id}", admin(token, c.inspectVersions))
	mux.HandleFunc("GET /api/admin/replicas/products/{product_id}/owners", admin(token, c.inspectOwners))
	return serveAPI(mux, fallback)
}

func (c Catalog) product(ctx context.Context, data []byte) (Product, error) {
	var p Product
	if err := json.Unmarshal(data, &p); err != nil {
		return Product{}, err
	}
	url, err := c.Images.Resolve(ctx, p.ImageURL)
	p.ImageURL = url
	return p, err
}

func (c Catalog) get(kind pb.DocumentKind, param string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		versions, err := readDocument(r.Context(), c.Replicas, kind, r.PathValue(param), r.Header.Get("X-Correlation-ID"))
		if err != nil {
			serviceError(w, r, err)
			return
		}
		c.writeView(w, r, kind, versions)
	}
}

// editView returns stored image references so editing a product does not persist a signed URL.
func (c Catalog) editView(w http.ResponseWriter, r *http.Request) {
	versions, err := readDocument(r.Context(), c.Replicas, productKind, r.PathValue("product_id"), r.Header.Get("X-Correlation-ID"))
	if err != nil {
		serviceError(w, r, err)
		return
	}
	if len(versions.Siblings) != 1 {
		c.writeView(w, r, productKind, versions)
		return
	}
	send(w, 200, map[string]any{"product": json.RawMessage(versions.Siblings[0].DocumentJson), "context": contextJSON(versions.Context)})
}

func (c Catalog) writeView(w http.ResponseWriter, r *http.Request, kind pb.DocumentKind, versions *pb.DocumentVersions) {
	switch len(versions.Siblings) {
	case 0:
		writeError(w, r, 404, "NOT_FOUND", "document not found")
	case 1:
		raw := versions.Siblings[0].DocumentJson
		if kind == saleKind {
			send(w, 200, json.RawMessage(raw))
			return
		}
		product, err := c.product(r.Context(), raw)
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "product image is unavailable")
			return
		}
		send(w, 200, map[string]any{"product": product, "context": contextJSON(versions.Context)})
	default:
		c.conflict(w, r, kind, versions)
	}
}

type productVersion struct {
	Product Product `json:"product"`
	Replica string  `json:"replica_id"`
	Counter string  `json:"counter"`
}

func productSiblings(versions *pb.DocumentVersions) ([]productVersion, error) {
	result := make([]productVersion, 0, len(versions.Siblings))
	for _, version := range versions.Siblings {
		var product Product
		if err := json.Unmarshal(version.DocumentJson, &product); err != nil {
			return nil, err
		}
		result = append(result, productVersion{product, version.Version.ReplicaId, strconv.FormatUint(version.Version.Counter, 10)})
	}
	return result, nil
}

func (c Catalog) conflict(w http.ResponseWriter, r *http.Request, kind pb.DocumentKind, versions *pb.DocumentVersions) {
	body := map[string]any{"code": "CATALOG_CONFLICT", "message": "document versions require explicit resolution",
		"correlation_id": r.Header.Get("X-Correlation-ID"), "context": contextJSON(versions.Context)}
	if kind == productKind {
		siblings, err := productSiblings(versions)
		if err != nil {
			serviceError(w, r, err)
			return
		}
		body["siblings"] = siblings
	}
	send(w, 409, body)
}

func (c Catalog) list(kind pb.DocumentKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values := []any{}
		req := &pb.ListDocumentsRequest{Kind: kind, Call: &pb.Call{CorrelationId: r.Header.Get("X-Correlation-ID")}}
		for {
			page, err := across(r.Context(), "a", func(id string) (*pb.DocumentPage, error) {
				return c.Replicas.List(r.Context(), id, req)
			})
			if err != nil {
				serviceError(w, r, err)
				return
			}
			for _, key := range page.Keys {
				versions, err := readDocument(r.Context(), c.Replicas, kind, key.Id, req.Call.CorrelationId)
				if err != nil {
					serviceError(w, r, err)
					return
				}
				if len(versions.Siblings) > 1 {
					c.conflict(w, r, kind, versions)
					return
				}
				if len(versions.Siblings) == 0 {
					continue
				}
				raw := versions.Siblings[0].DocumentJson
				if kind == saleKind {
					values = append(values, json.RawMessage(raw))
				} else {
					product, err := c.product(r.Context(), raw)
					if err != nil {
						writeError(w, r, 503, "UNAVAILABLE", "product image is unavailable")
						return
					}
					values = append(values, product)
				}
			}
			if page.NextCursor == "" {
				break
			}
			req.Cursor = page.NextCursor
		}
		send(w, 200, values)
	}
}

func (c Catalog) write(resolve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Operation string        `json:"operation_id"`
			Product   Product       `json:"product"`
			Context   causalContext `json:"context"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !validID(input.Operation) || !validID(input.Product.ID) || input.Product.ID != r.PathValue("product_id") ||
			!validID(input.Product.Name) || input.Context == nil || c.Images.Validate(input.Product.ImageURL) != nil {
			writeError(w, r, 400, "INVALID_ARGUMENT", "invalid operation, product, image, or context")
			return
		}
		observed, err := input.Context.protobuf()
		if err != nil {
			writeError(w, r, 400, "INVALID_ARGUMENT", err.Error())
			return
		}
		body, err := json.Marshal(input.Product)
		if err != nil {
			serviceError(w, r, err)
			return
		}
		ctx := r.Context()
		correlation := r.Header.Get("X-Correlation-ID")
		op := "product:" + input.Operation
		current, err := readDocument(ctx, c.Replicas, productKind, input.Product.ID, correlation)
		if err != nil {
			serviceError(w, r, err)
			return
		}
		replay := len(current.Siblings) == 1 && current.Siblings[0].OperationId == op &&
			bytes.Equal(current.Siblings[0].DocumentJson, body) && equivalentContext(current.Siblings[0].Context, observed)
		if !replay && (resolve && (len(current.Siblings) < 2 || !equivalentContext(current.Context, observed)) ||
			!resolve && len(current.Siblings) > 1) {
			c.conflict(w, r, productKind, current)
			return
		}
		envelope, err := c.Replicas.Prepare(ctx, originFor(op), &pb.PrepareMutationRequest{
			Call: &pb.Call{OperationId: op, CorrelationId: correlation},
			Key:  &pb.DocumentKey{Kind: productKind, Id: input.Product.ID}, Context: observed, DocumentJson: body,
		})
		if err == nil {
			err = applyDocument(ctx, c.Replicas, envelope, correlation)
		}
		if err != nil {
			serviceError(w, r, err)
			return
		}
		latest, err := readDocument(ctx, c.Replicas, productKind, input.Product.ID, correlation)
		if err != nil {
			serviceError(w, r, err)
			return
		}
		c.writeView(w, r, productKind, latest)
	}
}

func equivalentContext(left, right *pb.Context) bool {
	return len(left.GetObserved()) == 0 && len(right.GetObserved()) == 0 || proto.Equal(left, right)
}

func (c Catalog) inspect(w http.ResponseWriter, r *http.Request) {
	type replicaStatus struct {
		ID      string `json:"replica_id"`
		Ready   bool   `json:"backend_ready"`
		Pending uint64 `json:"pending_hints"`
	}
	all := make([]replicaStatus, 0, len(replicaOrder))
	for i := range len(replicaOrder) {
		id := string(replicaOrder[i])
		s, err := c.Replicas.Inspect(r.Context(), id, &pb.ReplicaStatusRequest{CorrelationId: r.Header.Get("X-Correlation-ID")})
		all = append(all, replicaStatus{id, err == nil && s.GetBackendReady(), s.GetPendingHints()})
	}
	send(w, 200, all)
}

func (c Catalog) inspectVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := readDocument(r.Context(), c.Replicas, productKind, r.PathValue("product_id"), r.Header.Get("X-Correlation-ID"))
	if err != nil {
		serviceError(w, r, err)
		return
	}
	siblings, err := productSiblings(versions)
	if err != nil {
		serviceError(w, r, err)
		return
	}
	send(w, 200, siblings)
}

func (c Catalog) inspectOwners(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("product_id")
	if !validID(id) {
		writeError(w, r, 400, "INVALID_ARGUMENT", "invalid product ID")
		return
	}
	send(w, 200, replica.PreferredOwners(&pb.DocumentKey{Kind: productKind, Id: id}))
}
