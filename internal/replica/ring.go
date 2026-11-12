package replica

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strconv"

	pb "flashkart/gen/flashkart/v1"
)

const (
	replicationFactor = 3
	readQuorum        = 2
	writeQuorum       = 2
	virtualNodes      = 128
)

var replicaIDs = [...]string{"a", "b", "c", "d"}

type ringPoint struct {
	token uint64
	id    string
}

var ring = func() []ringPoint {
	points := make([]ringPoint, 0, len(replicaIDs)*virtualNodes)
	for _, id := range replicaIDs {
		for i := range virtualNodes {
			points = append(points, ringPoint{hashToken(id + ":" + strconv.Itoa(i)), id})
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].token < points[j].token })
	return points
}()

func hashToken(value string) uint64 {
	sum := sha256.Sum256([]byte(value))
	return binary.BigEndian.Uint64(sum[:8])
}

// PreferredOwners returns the permanent, distinct owners in clockwise ring order.
func PreferredOwners(key *pb.DocumentKey) []string {
	token := hashToken(strconv.Itoa(int(key.GetKind())) + ":" + key.GetId())
	start := sort.Search(len(ring), func(i int) bool { return ring[i].token >= token })
	owners := make([]string, 0, replicationFactor)
	seen := map[string]bool{}
	for i := 0; len(owners) < replicationFactor; i++ {
		id := ring[(start+i)%len(ring)].id
		if !seen[id] {
			seen[id] = true
			owners = append(owners, id)
		}
	}
	return owners
}
