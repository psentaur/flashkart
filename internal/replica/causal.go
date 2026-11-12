package replica

import (
	"math"
	"sort"

	pb "flashkart/gen/flashkart/v1"
)

func replicaID(id string) bool {
	return id == "a" || id == "b" || id == "c" || id == "d"
}

func validContext(ctx *pb.Context) bool {
	for id, counters := range ctx.GetObserved() {
		if !replicaID(id) || len(counters.GetIntervals()) == 0 {
			return false
		}
		var previous uint64
		for _, span := range counters.GetIntervals() {
			if span.GetFirst() <= previous || span.GetLast() < span.GetFirst() || span.GetLast() > math.MaxInt64 {
				return false
			}
			previous = span.GetLast()
		}
	}
	return true
}

func observes(ctx *pb.Context, dot *pb.Dot) bool {
	spans := ctx.GetObserved()[dot.GetReplicaId()].GetIntervals()
	i := sort.Search(len(spans), func(i int) bool { return spans[i].GetLast() >= dot.GetCounter() })
	return i < len(spans) && spans[i].GetFirst() <= dot.GetCounter()
}

func combined(versions []*pb.Mutation) *pb.Context {
	ctx := &pb.Context{Observed: map[string]*pb.Counters{}}
	add := func(id string, first, last uint64) {
		if ctx.Observed[id] == nil {
			ctx.Observed[id] = &pb.Counters{}
		}
		c := ctx.Observed[id]
		c.Intervals = append(c.Intervals, &pb.Interval{First: first, Last: last})
	}
	for _, version := range versions {
		for id, counters := range version.GetContext().GetObserved() {
			for _, span := range counters.GetIntervals() {
				add(id, span.First, span.Last)
			}
		}
		dot := version.GetVersion()
		add(dot.ReplicaId, dot.Counter, dot.Counter)
	}
	for _, counters := range ctx.Observed {
		sort.Slice(counters.Intervals, func(i, j int) bool {
			return counters.Intervals[i].First < counters.Intervals[j].First
		})
		merged := counters.Intervals[:0]
		for _, span := range counters.Intervals {
			if n := len(merged); n > 0 && span.First <= merged[n-1].Last+1 {
				merged[n-1].Last = max(merged[n-1].Last, span.Last)
			} else {
				merged = append(merged, span)
			}
		}
		counters.Intervals = merged
	}
	return ctx
}
