package market

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestCatalogAndPublication(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_MARKET") != "1" {
		t.Skip("run make test-catalog-publication against the local PostgreSQL stack")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	productID, operation, publishOp := rand.Text(), rand.Text(), rand.Text()
	var outage string
	for _, id := range []string{"a", "b", "c", "d"} {
		if id != originFor("product:"+operation) && id != originFor("sale:"+publishOp) {
			outage = id
			break
		}
	}

	var fault atomic.Value
	fault.Store("")
	prepared := make(chan *pb.Mutation, 1)
	interceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		reply, err := next(ctx, req)
		if err == nil && fault.Load() == info.FullMethod {
			if m, ok := reply.(*pb.Mutation); ok {
				prepared <- m
			}
			return nil, status.Error(codes.Aborted, "test reply lost after remote commit")
		}
		return reply, err
	}
	addresses := map[string]string{}
	listeners := map[string]net.Listener{}
	for _, id := range []string{"a", "b", "c", "d"} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		addresses[id], listeners[id] = listener.Addr().String(), listener
	}
	if err := listeners[outage].Close(); err != nil {
		t.Fatal(err)
	}
	client, err := replica.NewClient(addresses, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	for id, listener := range listeners {
		if id == outage {
			continue
		}
		store, err := replica.Open(ctx, os.Getenv("FLASHKART_REPLICA_"+strings.ToUpper(id)+"_DSN"), id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(store.Close)
		if !store.Ready(ctx) {
			t.Fatalf("replica %s is unavailable", id)
		}
		server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
		t.Cleanup(server.Stop)
		pb.RegisterReplicaPeerServer(server, store)
		pb.RegisterCatalogReplicationServer(server, replica.NewCoordinator(store, client, 200*time.Millisecond, time.Second))
		go server.Serve(listener)
	}
	openDB := func(variable string) *pgxpool.Pool {
		t.Helper()
		dsn := os.Getenv(variable)
		if dsn == "" {
			t.Fatalf("missing %s", variable)
		}
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	inventoryDB := openDB("FLASHKART_INVENTORY_DSN")
	inventory := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
	t.Cleanup(inventory.Stop)
	pb.RegisterInventoryServer(inventory, Inventory{DB: inventoryDB})
	inventoryListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go inventory.Serve(inventoryListener)
	conn, err := grpc.NewClient("passthrough:///"+inventoryListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	inventoryClient := pb.NewInventoryClient(conn)
	publisher := func() Publisher {
		return Publisher{DB: openDB("FLASHKART_FLASH_SALE_DSN"), Replicas: client, Inventory: inventoryClient,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	manager := publisher()
	catalog := Catalog{Replicas: client}.Handler("admin-token", http.NotFoundHandler())
	managerHTTP := manager.Handler("admin-token", http.NotFoundHandler())
	request := func(handler http.Handler, method, path string, body any, token string, want int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data)).WithContext(ctx)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("X-Correlation-ID", "phase5-test")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}

	product := Product{ID: productID, Name: "Original product"}
	edit := map[string]any{"operation_id": operation, "product": product, "context": causalContext{}}
	path := "/api/admin/products/" + productID
	request(catalog, "PUT", path, edit, "", 401)
	request(catalog, "GET", "/api/admin/replicas", nil, "customer-token", 403)
	request(catalog, "PUT", path, edit, "admin-token", 200)
	request(catalog, "PUT", path, edit, "admin-token", 200)
	request(catalog, "GET", "/api/products/"+productID, nil, "", 200)

	product.Name = "Concurrent product"
	body, err := json.Marshal(product)
	if err != nil {
		t.Fatal(err)
	}
	other := originFor("product:" + operation)
	sibling, err := client.Prepare(ctx, other, &pb.PrepareMutationRequest{Call: &pb.Call{OperationId: rand.Text()},
		Key: &pb.DocumentKey{Kind: productKind, Id: productID}, DocumentJson: body})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyDocument(ctx, client, sibling, ""); err != nil {
		t.Fatal(err)
	}
	conflict := request(catalog, "GET", "/api/products/"+productID, nil, "", 409)
	var message struct {
		Code     string           `json:"code"`
		Context  causalContext    `json:"context"`
		Siblings []productVersion `json:"siblings"`
	}
	if err := json.Unmarshal(conflict, &message); err != nil || message.Code != "CATALOG_CONFLICT" || len(message.Siblings) != 2 {
		t.Fatalf("invalid conflict response: %s", conflict)
	}
	resolveOp := rand.Text()
	for originFor("product:"+resolveOp) == outage {
		resolveOp = rand.Text()
	}
	resolution := map[string]any{"operation_id": resolveOp, "product": product, "context": message.Context}
	request(catalog, "PUT", path, resolution, "admin-token", 409)
	request(catalog, "POST", path+"/resolve", resolution, "admin-token", 200)
	request(catalog, "GET", "/api/products/"+productID, nil, "", 200)

	price := int64(9500)
	terms := Terms{ProductID: productID, PriceMinor: &price, Currency: "INR", Stock: 20,
		StartsAt: "2027-01-02T10:00:00Z", EndsAt: "2027-01-02T12:00:00Z"}
	created := request(managerHTTP, "POST", "/api/admin/sale-drafts", terms, "admin-token", 201)
	var saleDraft draft
	if err := json.Unmarshal(created, &saleDraft); err != nil || saleDraft.State != "DRAFT" {
		t.Fatalf("invalid draft: %s", created)
	}
	draftPath := "/api/admin/sale-drafts/" + saleDraft.ID
	terms.Stock = 22
	request(managerHTTP, "PATCH", draftPath, terms, "admin-token", 200)
	publish := map[string]string{"operation_id": publishOp}
	request(managerHTTP, "POST", draftPath+"/publish", publish, "admin-token", 202)
	request(managerHTTP, "PATCH", draftPath, terms, "admin-token", 409)
	request(managerHTTP, "POST", draftPath+"/publish", map[string]string{"operation_id": rand.Text()}, "admin-token", 409)
	jobState := func(p Publisher) job {
		t.Helper()
		var j job
		err := p.DB.QueryRow(ctx, `SELECT sale_id,terms,operation_id,origin,step,envelope,correlation_id
			FROM flash_sale.drafts WHERE sale_id=$1`, saleDraft.ID).Scan(&j.ID, &j.Terms, &j.Operation, &j.Origin, &j.Step, &j.Envelope, &j.Correlation)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	var lostEnvelope *pb.Mutation
	for stage, method := range []string{pb.Inventory_PrepareSale_FullMethodName, pb.CatalogReplication_PrepareMutation_FullMethodName,
		pb.CatalogReplication_ApplyMutation_FullMethodName, pb.Inventory_ActivateSale_FullMethodName} {
		j := jobState(manager)
		if j.Step != stage || j.Origin != originFor("sale:"+publishOp) {
			t.Fatalf("unexpected checkpoint %d or changed origin at step %d", j.Step, stage)
		}
		fault.Store(method)
		if err := manager.advance(ctx, j); status.Code(err) != codes.Aborted {
			t.Fatalf("lost reply at step %d: %v", stage, err)
		}
		fault.Store("")
		if stage == stepPrepare {
			lostEnvelope = <-prepared
		}
		manager.DB.Close()
		manager = publisher()
		j = jobState(manager)
		if j.Step != stage {
			t.Fatalf("step advanced without a confirmed reply: %d", j.Step)
		}
		if stage != stepActivate {
			if err := manager.advance(ctx, j); err != nil {
				t.Fatalf("resume step %d: %v", stage, err)
			}
		}
		if stage == stepPrepare {
			var envelope pb.Mutation
			if err := proto.Unmarshal(jobState(manager).Envelope, &envelope); err != nil || !proto.Equal(&envelope, lostEnvelope) {
				t.Fatalf("retry changed the prepared envelope: %v", err)
			}
		}
	}
	workerCtx, stop := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() { defer close(finished); manager.Run(workerCtx) }()
	defer func() { stop(); <-finished }()
	for jobState(manager).Step != stepPublished {
		select {
		case <-ctx.Done():
			t.Fatal("publication worker did not resume activation")
		case <-time.After(25 * time.Millisecond):
		}
	}
	managerHTTP = manager.Handler("admin-token", http.NotFoundHandler())
	result := request(managerHTTP, "GET", "/api/admin/publications/"+publishOp, nil, "admin-token", 200)
	if !bytes.Contains(result, []byte(`"state":"PUBLISHED"`)) {
		t.Fatalf("unexpected publication status: %s", result)
	}
	request(managerHTTP, "POST", draftPath+"/publish", publish, "admin-token", 202)
	request(managerHTTP, "PATCH", draftPath, terms, "admin-token", 409)
	sale := request(catalog, "GET", "/api/sales/"+saleDraft.ID, nil, "", 200)
	var active bool
	var inventoryTerms Terms
	var available int
	if err := inventoryDB.QueryRow(ctx, `SELECT active,terms,available_stock FROM inventory.sales WHERE sale_id=$1`,
		saleDraft.ID).Scan(&active, &inventoryTerms, &available); err != nil || !active || available != int(terms.Stock) {
		t.Fatalf("incorrect inventory activation: %v %v", active, err)
	}
	var catalogSale Sale
	if err := json.Unmarshal(sale, &catalogSale); err != nil || !proto.Equal(catalogSale.protobuf(), (Sale{saleDraft.ID, inventoryTerms}).protobuf()) {
		t.Fatalf("catalog and inventory terms differ: %s %v", sale, err)
	}
	var listed []Sale
	result = request(catalog, "GET", "/api/sales", nil, "", 200)
	if err := json.Unmarshal(result, &listed); err != nil || !slices.ContainsFunc(listed, func(s Sale) bool { return s.ID == saleDraft.ID }) {
		t.Fatalf("published sale missing from discovery: %v", err)
	}
	changed := catalogSale.protobuf()
	changed.Stock++
	if _, err := inventoryClient.PrepareSale(ctx, &pb.PrepareSaleRequest{Call: &pb.Call{OperationId: publishOp}, Terms: changed}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("inventory allowed changed terms: %v", err)
	}
}
