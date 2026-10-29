/*
Copyright 2026 Peter Severin Rasmussen.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package simply_test

import (
	"context"
	"testing"

	"github.com/PeterSR/simply-com-operator/internal/simply"
	"github.com/PeterSR/simply-com-operator/internal/simply/simplytest"
)

//go:fix inline

func TestRecordLifecycle(t *testing.T) {
	srv := simplytest.NewServer("key", "example.com")
	defer srv.Close()
	c := simply.NewClient("key", simply.WithBaseURL(srv.BaseURL()))
	ctx := context.Background()

	id, err := c.CreateDNSRecord(ctx, "example.com", simply.DNSRecord{
		Name: "www.example.com", Type: "CNAME", Data: "target.example.net.", TTL: new(int32(600)), Comment: new("hello"),
	})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := c.ListDNSRecords(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ID != id || recs[0].Name != "www" || recs[0].Data != "target.example.net" ||
		*recs[0].TTL != 600 || *recs[0].Comment != "hello" {
		t.Fatalf("unexpected records %+v", recs)
	}

	if err := c.UpdateDNSRecord(ctx, "example.com", id, simply.DNSRecord{Name: "www", Type: "A", Data: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	recs, _ = c.ListDNSRecords(ctx, "example.com")
	if recs[0].Type != "A" || recs[0].Comment != nil {
		t.Fatalf("PUT should replace the whole record, got %+v", recs[0])
	}
	if err := c.ReloadDNSZone(ctx, "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDNSRecord(ctx, "example.com", id); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDNSRecord(ctx, "example.com", id); !simply.IsNotFound(err) {
		t.Fatalf("second delete: want not found, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	srv := simplytest.NewServer("key", "example.com")
	defer srv.Close()
	ctx := context.Background()

	bad := simply.NewClient("wrong", simply.WithBaseURL(srv.BaseURL()))
	if _, err := bad.ListProducts(ctx); !simply.IsUnauthorized(err) {
		t.Fatalf("want unauthorized, got %v", err)
	}

	c := simply.NewClient("key", simply.WithBaseURL(srv.BaseURL()))
	if _, err := c.ListDNSRecords(ctx, "other.example"); !simply.IsNotFound(err) {
		t.Fatalf("want not found, got %v", err)
	}
	srv.Seed("example.com", simply.DNSRecord{Name: "x", Type: "A", Data: "192.0.2.1"})
	_, err := c.CreateDNSRecord(ctx, "example.com", simply.DNSRecord{Name: "x", Type: "CNAME", Data: "example.net"})
	if !simply.IsClientError(err) || err.Error() != "simply.com API: HTTP 400: Bad Request" {
		t.Fatalf("want a 400 with the problem detail, got %v", err)
	}

	products, err := c.ListProducts(ctx)
	if err != nil || len(products) != 1 || products[0].Object != "example.com" {
		t.Fatalf("products: %v %+v", err, products)
	}
}
