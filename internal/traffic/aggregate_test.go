package traffic

import (
	"testing"
	"time"

	native "github.com/chainreactors/cyber/aop/traffic"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func flowFixture(id, url string, status int32) *native.Flow {
	return &native.Flow{
		Id: id, Complete: true,
		Request:  &native.HttpRequest{Method: "GET", Url: url},
		Response: &native.HttpResponse{StatusCode: status},
	}
}

// The aggregate is one pass over the immutable index: hosts bucket requests,
// unique paths count as URLs, and each URL keeps the newest exchange's
// summary and verdict. The index is read in hash-name order, so "newest" must
// be decided by flow time, not read order.
func TestAggregateByHostAndURL(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	older := &native.Flow{Id: "f-old", Complete: true,
		Request:   &native.HttpRequest{Method: "GET", Url: "https://www.example.com/a"},
		Response:  &native.HttpResponse{StatusCode: 200, Body: []byte("ok")},
		Timestamp: timestamppb.New(base)}
	newer := &native.Flow{Id: "f-new", Complete: true,
		Request:   &native.HttpRequest{Method: "GET", Url: "https://www.example.com/a"},
		Response:  &native.HttpResponse{StatusCode: 404, Body: []byte("missing1")},
		Timestamp: timestamppb.New(base.Add(30 * time.Minute))}
	if _, err := SaveCapture(dir, "run-1", "in_1", "s", "e-1", older, nil, "record", "sr-1", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveCapture(dir, "run-2", "in_2", "s", "e-2", newer, nil, "review", "sr-2", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveCapture(dir, "run-3", "in_1", "s", "e-3", flowFixture("f-b", "https://api.example.com/b", 500), nil, "", "", false, false); err != nil {
		t.Fatal(err)
	}

	agg := new(HostAggregator)
	hosts, order := agg.Hosts(dir)
	if len(hosts) != 2 || len(order) != 2 {
		t.Fatalf("hosts = %+v order=%v", hosts, order)
	}
	www := hosts["www.example.com"]
	if www == nil || www.Requests != 2 || www.URLs != 1 || www.Statuses["2xx"] != 1 || www.Statuses["4xx"] != 1 {
		t.Fatalf("www aggregate = %+v", www)
	}
	if len(www.Intents) != 2 {
		t.Fatalf("intents = %+v", www.Intents)
	}

	urls := agg.URLs(dir, "www.example.com")
	if len(urls) != 1 {
		t.Fatalf("urls = %+v", urls)
	}
	u := urls[0]
	if u.Verdict != "review" || u.RuleID != "sr-2" || !u.Missed {
		t.Fatalf("newest verdict by flow time, got %+v", u)
	}
	if u.LastStatus != 404 || u.LastBytes != int(len("missing1")) {
		t.Fatalf("newest response summary = %+v", u)
	}

	// The cache answers from the signature until the index grows.
	hosts2, _ := agg.Hosts(dir)
	if len(hosts2) != 2 {
		t.Fatalf("cached read changed the shape: %+v", hosts2)
	}
	if _, err := SaveCapture(dir, "run-4", "in_1", "s", "e-4", flowFixture("f-c", "https://third.example.com/c", 200), nil, "", "", false, false); err != nil {
		t.Fatal(err)
	}
	hosts3, _ := agg.Hosts(dir)
	if len(hosts3) != 3 || hosts3["third.example.com"] == nil {
		t.Fatalf("a new capture did not invalidate the cache: %+v", hosts3)
	}
}
