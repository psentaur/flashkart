package main

import "testing"

func TestReadyMustMatchProcessIdentity(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"component":"gateway","revision":"test","mode":"foundation","business_ready":false,"capabilities":["us1-browsing"]}`, true},
		{`{"component":"order","revision":"test","mode":"foundation"}`, false},
		{`{"component":"gateway","revision":"wrong","mode":"foundation"}`, false},
		{`{"component":"gateway","revision":"test","mode":"foundation","capabilities":[]}`, false},
		{`{"component":"gateway","revision":"test","mode":"foundation","business_ready":true}`, false},
	} {
		if matchesHealth([]byte(tc.body), "gateway", "test") != tc.want {
			t.Fatal(tc.body)
		}
	}
}
