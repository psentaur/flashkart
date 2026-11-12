package replica

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type Client struct {
	connections map[string]*grpc.ClientConn
	timeout     time.Duration
}

// NewClient opens reusable channels for a fixed cluster on its private network.
func NewClient(addresses map[string]string, timeout time.Duration) (*Client, error) {
	if len(addresses) != len(replicaIDs) || timeout <= 0 {
		return nil, fmt.Errorf("four replica endpoints and a positive timeout are required")
	}
	client := &Client{connections: make(map[string]*grpc.ClientConn), timeout: timeout}
	seen := map[string]bool{}
	for _, id := range replicaIDs {
		address := addresses[id]
		host, port, err := net.SplitHostPort(address)
		number, portErr := strconv.Atoi(port)
		if err != nil || host == "" || portErr != nil || number <= 0 || number > 65535 || seen[address] {
			client.Close()
			return nil, fmt.Errorf("invalid or duplicate endpoint for replica %s", id)
		}
		conn, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			client.Close()
			return nil, err
		}
		client.connections[id] = conn
		seen[address] = true
	}
	return client, nil
}

func (c *Client) Close() {
	for _, conn := range c.connections {
		conn.Close()
	}
}

func (c *Client) catalog(id string) (pb.CatalogReplicationClient, error) {
	conn := c.connections[id]
	if conn == nil {
		return nil, status.Error(codes.InvalidArgument, "unknown replica identity")
	}
	return pb.NewCatalogReplicationClient(conn), nil
}

// Prepare always uses the supplied origin, including on retries.
func (c *Client) Prepare(ctx context.Context, origin string, req *pb.PrepareMutationRequest) (*pb.Mutation, error) {
	api, err := c.catalog(origin)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return api.PrepareMutation(ctx, req)
}

func (c *Client) Apply(ctx context.Context, coordinator string, req *pb.ApplyMutationRequest) (*pb.Ack, error) {
	api, err := c.catalog(coordinator)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return api.ApplyMutation(ctx, req)
}

func (c *Client) Read(ctx context.Context, coordinator string, req *pb.ReadDocumentRequest) (*pb.DocumentVersions, error) {
	api, err := c.catalog(coordinator)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return api.ReadDocument(ctx, req)
}

func (c *Client) List(ctx context.Context, coordinator string, req *pb.ListDocumentsRequest) (*pb.DocumentPage, error) {
	api, err := c.catalog(coordinator)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return api.ListDocuments(ctx, req)
}

func (c *Client) Inspect(ctx context.Context, id string, req *pb.ReplicaStatusRequest) (*pb.ReplicaStatus, error) {
	conn := c.connections[id]
	if conn == nil {
		return nil, status.Error(codes.InvalidArgument, "unknown replica identity")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return pb.NewReplicaPeerClient(conn).Inspect(ctx, req)
}
