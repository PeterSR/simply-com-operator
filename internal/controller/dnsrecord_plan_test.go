/*
Copyright 2026 The simply-com-operator Authors.

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

package controller

import (
	"testing"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

//go:fix inline

func TestRelativeName(t *testing.T) {
	for in, want := range map[string]string{
		"www": "www", "WWW.Example.com.": "www", "example.com": "@", "@": "@", "": "@",
		"*.example.com": "*", "a.b": "a.b", "_dmarc.example.com": "_dmarc",
	} {
		if got := relativeName("example.com", in); got != want {
			t.Errorf("relativeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	c := commentWithMarker("web server", "prod", "ns/www")
	if c != "web server [simply-com-operator owner=prod ref=ns/www]" {
		t.Fatalf("comment %q", c)
	}
	o, r, ok := parseMarker(&c)
	if !ok || o != "prod" || r != "ns/www" {
		t.Fatalf("parse: %v %q %q", ok, o, r)
	}
	if got := commentWithoutMarker(&c); got == nil || *got != "web server" {
		t.Fatalf("without marker: %v", got)
	}
	bare := marker("prod", "ns/www")
	if commentWithoutMarker(&bare) != nil {
		t.Fatal("a bare marker should leave no comment")
	}
	for _, s := range []string{"", "web", "[simply-com-operator owner=a]", "x [simply-com-operator owner=a ref=b/c] trailing"} {
		if _, _, ok := parseMarker(&s); ok {
			t.Errorf("%q parsed as a marker", s)
		}
	}
}

func TestNormalizeData(t *testing.T) {
	cases := []struct{ typ, a, b string }{
		{"CNAME", "Target.Example.net.", "target.example.net"},
		{"AAAA", "2001:DB8::0:1", "2001:db8::1"},
		{"TXT", "\"v=spf1 -all\"", "v=spf1 -all"},
		{"SRV", "10 443 Host.Example.com.", "10 443 host.example.com"},
	}
	for _, c := range cases {
		if normalizeData(c.typ, c.a) != normalizeData(c.typ, c.b) {
			t.Errorf("%s %q and %q should be equal", c.typ, c.a, c.b)
		}
	}
}

func TestValidateSpec(t *testing.T) {
	ok := []simplycomv1alpha1.DNSRecordSpec{
		{Zone: "example.com", Name: "www", Type: "A", Data: "192.0.2.1"},
		{Zone: "example.com", Name: "*", Type: "AAAA", Data: "2001:db8::1"},
		{Zone: "example.com", Name: "@", Type: "MX", Data: "mail.example.com", Priority: new(int32(10))},
		{Zone: "example.com", Name: "_sip._tcp", Type: "SRV", Data: "10 5060 sip.example.com", Priority: new(int32(0))},
		{Zone: "example.com", Name: "*.apps", Type: "CNAME", Data: "lb.example.net"},
	}
	for _, s := range ok {
		if err := validateSpec(&s); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	bad := []simplycomv1alpha1.DNSRecordSpec{
		{Zone: "example.com", Name: "www", Type: "A", Data: "2001:db8::1"},
		{Zone: "example.com", Name: "www", Type: "AAAA", Data: "192.0.2.1"},
		{Zone: "example.com", Name: "@", Type: "CNAME", Data: "x.example.net"},
		{Zone: "example.com", Name: "a b", Type: "TXT", Data: "x"},
		{Zone: "example.com", Name: "a.*", Type: "TXT", Data: "x"},
		{Zone: "example.com", Name: "_sip._tcp", Type: "SRV", Data: "sip.example.com", Priority: new(int32(0))},
		{Zone: "example.com", Name: "x", Type: "TXT", Data: "x", Comment: "[simply-com-operator owner=a ref=b]"},
	}
	for _, s := range bad {
		if err := validateSpec(&s); err == nil {
			t.Errorf("%+v: want an error", s)
		}
	}
}

func TestPlanRecord(t *testing.T) {
	spec := simplycomv1alpha1.DNSRecordSpec{Zone: "example.com", Name: "www", Type: "A", Data: "192.0.2.1", TTL: new(int32(600))}
	want := desiredRecord(&spec, "me", "ns/www")
	ours := func(id int64, data string) simply.DNSRecord {
		return simply.DNSRecord{ID: id, Name: "www", Type: "A", Data: data, TTL: new(int32(600)), Comment: new(marker("me", "ns/www"))}
	}
	foreign := func(id int64, typ, data, comment string) simply.DNSRecord {
		r := simply.DNSRecord{ID: id, Name: "www", Type: typ, Data: data, TTL: new(int32(600))}
		if comment != "" {
			r.Comment = &comment
		}
		return r
	}

	cases := []struct {
		name       string
		zone       []simply.DNSRecord
		adopt      bool
		action     planAction
		id         int64
		duplicates int
	}{
		{"empty zone creates", nil, false, actionCreate, 0, 0},
		{"in sync does nothing", []simply.DNSRecord{ours(1, "192.0.2.1")}, false, actionNone, 1, 0},
		{"drift updates", []simply.DNSRecord{ours(1, "192.0.2.9")}, false, actionUpdate, 1, 0},
		{"duplicates removed", []simply.DNSRecord{ours(1, "192.0.2.9"), ours(2, "192.0.2.1")}, false, actionNone, 2, 1},
		{"other A values left alone", []simply.DNSRecord{foreign(5, "A", "192.0.2.2", "")}, false, actionCreate, 0, 0},
		{"unmanaged match conflicts", []simply.DNSRecord{foreign(5, "A", "192.0.2.1", "web")}, false, actionConflict, 0, 0},
		{"unmanaged match adopted", []simply.DNSRecord{foreign(5, "A", "192.0.2.1", "web")}, true, actionUpdate, 5, 0},
		{"other owner conflicts even with adopt", []simply.DNSRecord{foreign(5, "A", "192.0.2.1", marker("other", "ns/www"))}, true, actionConflict, 0, 0},
		{"update into a copy of an unmanaged record conflicts",
			[]simply.DNSRecord{ours(1, "192.0.2.9"), foreign(5, "A", "192.0.2.1", "")}, false, actionConflict, 0, 0},
		{"unmanaged CNAME conflicts", []simply.DNSRecord{foreign(5, "CNAME", "x.example.net", "")}, true, actionConflict, 0, 0},
	}
	for _, c := range cases {
		p := planRecord(c.zone, want, "me", "ns/www", nil, c.adopt)
		if p.action != c.action || (c.id != 0 && p.recordID != c.id) || len(p.duplicates) != c.duplicates {
			t.Errorf("%s: got %+v", c.name, p)
		}
	}
}

func TestPlanCNAMEOverOwnRecord(t *testing.T) {
	spec := simplycomv1alpha1.DNSRecordSpec{Zone: "example.com", Name: "www", Type: "CNAME", Data: "lb.example.net"}
	want := desiredRecord(&spec, "me", "ns/www")
	zone := []simply.DNSRecord{{ID: 1, Name: "www", Type: "A", Data: "192.0.2.1", Comment: new(marker("me", "ns/www"))}}
	p := planRecord(zone, want, "me", "ns/www", nil, false)
	if p.action != actionUpdate || p.recordID != 1 {
		t.Fatalf("changing the type of an owned record should update it, got %+v", p)
	}
}

func TestUpdatePayloadKeepsUnsetTTL(t *testing.T) {
	spec := simplycomv1alpha1.DNSRecordSpec{Zone: "example.com", Name: "www", Type: "A", Data: "192.0.2.1"}
	want := desiredRecord(&spec, "me", "ns/www")
	got := updatePayload(simply.DNSRecord{TTL: new(int32(900)), Type: "A"}, want)
	if got.TTL == nil || *got.TTL != 900 {
		t.Fatalf("TTL not carried over: %+v", got)
	}
}
