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

// Package simplytest is an in-memory stand-in for the parts of the Simply.com API the operator
// uses, for tests. It mimics the behaviour seen on the real API: names come back relative to the
// zone, a trailing dot is dropped from host names, quotes are stripped from TXT data, a PUT
// replaces the whole record (a missing comment clears it), exact duplicates are accepted, and a
// CNAME next to another record at the same name is refused.
package simplytest

import (
	"cmp"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/PeterSR/simply-com-operator/internal/simply"
)

// Server is a fake Simply.com API.
type Server struct {
	*httptest.Server

	mu      sync.Mutex
	apiKey  string
	zones   map[string]map[int64]simply.DNSRecord
	nextID  int64
	reloads map[string]int
	calls   map[string]int
	// failReloads is how many reloads to refuse before accepting them again.
	failReloads int
}

// NewServer starts a fake that accepts apiKey and serves the given zones, initially empty.
func NewServer(apiKey string, zones ...string) *Server {
	s := &Server{
		apiKey:  apiKey,
		zones:   map[string]map[int64]simply.DNSRecord{},
		nextID:  1000,
		reloads: map[string]int{},
		calls:   map[string]int{},
	}
	for _, z := range zones {
		s.zones[z] = map[int64]simply.DNSRecord{}
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// BaseURL is the URL to hand to simply.WithBaseURL.
func (s *Server) BaseURL() string { return s.URL + "/2" }

// Seed adds a record directly, as if made in the control panel, and returns its id.
func (s *Server) Seed(zone string, r simply.DNSRecord) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	r.ID = s.nextID
	s.zones[zone][r.ID] = normalize(zone, r)
	return r.ID
}

// Records returns the records of zone, ordered by id.
func (s *Server) Records(zone string) []simply.DNSRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]simply.DNSRecord, 0, len(s.zones[zone]))
	for _, r := range s.zones[zone] {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b simply.DNSRecord) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Set replaces a record directly, as if edited in the control panel.
func (s *Server) Set(zone string, r simply.DNSRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.zones[zone][r.ID] = normalize(zone, r)
}

// Reloads is how many times zone has been reloaded.
func (s *Server) Reloads(zone string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloads[zone]
}

// FailReloads makes the next n reloads answer 503.
func (s *Server) FailReloads(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failReloads = n
}

// Calls is how many requests have been made with the given method, such as "POST".
func (s *Server) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func normalize(zone string, r simply.DNSRecord) simply.DNSRecord {
	r.Name = strings.TrimSuffix(strings.ToLower(r.Name), ".")
	if r.Name == zone {
		r.Name = "@"
	}
	r.Name = strings.TrimSuffix(r.Name, "."+zone)
	switch r.Type {
	case "CNAME", "MX", "NS", "ALIAS":
		r.Data = strings.TrimSuffix(r.Data, ".")
	case "TXT":
		r.Data = strings.ReplaceAll(r.Data, "\"", "")
	}
	if r.TTL == nil {
		ttl := int32(3600)
		r.TTL = &ttl
	}
	if r.Comment != nil && *r.Comment == "" {
		r.Comment = nil
	}
	return r
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, code int, detail string) {
	writeJSON(w, code, map[string]any{"title": "An error occurred", "status": code, "detail": detail})
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[r.Method]++

	if _, key, ok := r.BasicAuth(); !ok || key != s.apiKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// 2 my products ...
	if len(parts) < 3 || parts[0] != "2" || parts[1] != "my" || parts[2] != "products" {
		problem(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(parts) == 3 && r.Method == http.MethodGet {
		var products []map[string]any
		names := make([]string, 0, len(s.zones))
		for z := range s.zones {
			names = append(names, z)
		}
		slices.Sort(names)
		for _, z := range names {
			products = append(products, map[string]any{"object": z, "name": z, "domain": map[string]any{"name": z}})
		}
		writeJSON(w, http.StatusOK, map[string]any{"products": products})
		return
	}
	if len(parts) < 5 || parts[4] != "dns" {
		problem(w, http.StatusNotFound, "Not Found")
		return
	}
	zone := parts[3]
	records, ok := s.zones[zone]
	if !ok {
		problem(w, http.StatusNotFound, "Not Found")
		return
	}
	rest := parts[5:]
	switch {
	case len(rest) == 1 && rest[0] == "reload" && r.Method == http.MethodPost:
		if s.failReloads > 0 {
			s.failReloads--
			problem(w, http.StatusServiceUnavailable, "Service Unavailable")
			return
		}
		s.reloads[zone]++
		writeJSON(w, http.StatusOK, map[string]any{"status": 200, "message": "success"})
	case len(rest) == 1 && rest[0] == "records" && r.Method == http.MethodGet:
		list := make([]simply.DNSRecord, 0, len(records))
		for _, rec := range records {
			list = append(list, rec)
		}
		slices.SortFunc(list, func(a, b simply.DNSRecord) int { return cmp.Compare(a.ID, b.ID) })
		writeJSON(w, http.StatusOK, map[string]any{"records": list, "status": 200, "message": "success"})
	case len(rest) == 1 && rest[0] == "records" && r.Method == http.MethodPost:
		rec, ok := decode(w, r, zone)
		if !ok {
			return
		}
		if cnameClash(records, rec, 0) {
			problem(w, http.StatusBadRequest, "Bad Request")
			return
		}
		s.nextID++
		rec.ID = s.nextID
		records[rec.ID] = rec
		writeJSON(w, http.StatusOK, map[string]any{"record": map[string]any{"id": rec.ID}, "status": 200, "message": "success"})
	case len(rest) == 2 && rest[0] == "records":
		id, err := strconv.ParseInt(rest[1], 10, 64)
		if _, exists := records[id]; err != nil || !exists {
			problem(w, http.StatusNotFound, "Not Found")
			return
		}
		switch r.Method {
		case http.MethodPut:
			rec, ok := decode(w, r, zone)
			if !ok {
				return
			}
			if cnameClash(records, rec, id) {
				problem(w, http.StatusBadRequest, "Bad Request")
				return
			}
			rec.ID = id
			records[id] = rec
		case http.MethodDelete:
			delete(records, id)
		default:
			problem(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": 200, "message": "success"})
	default:
		problem(w, http.StatusNotFound, "Not Found")
	}
}

func decode(w http.ResponseWriter, r *http.Request, zone string) (simply.DNSRecord, bool) {
	var rec simply.DNSRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil || rec.Name == "" || rec.Type == "" || rec.Data == "" {
		problem(w, http.StatusBadRequest, "Bad Request")
		return rec, false
	}
	rec.ID = 0
	return normalize(zone, rec), true
}

func cnameClash(records map[int64]simply.DNSRecord, rec simply.DNSRecord, skip int64) bool {
	for id, other := range records {
		if id == skip || other.Name != rec.Name {
			continue
		}
		if rec.Type == "CNAME" || other.Type == "CNAME" {
			return true
		}
	}
	return false
}
