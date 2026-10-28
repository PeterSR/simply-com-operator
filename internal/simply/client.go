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

// Package simply is a small client for the Simply.com API, version 2
// (https://www.simply.com/docs/api/). It covers what the operator uses.
package simply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// DefaultBaseURL is the Simply.com API endpoint.
const DefaultBaseURL = "https://api.simply.com/2"

// Client talks to the Simply.com API with one account's API key.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *rate.Limiter
	userAgent  string
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another endpoint, such as a test server.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithRateLimiter makes every request wait for the limiter first. Share one limiter between clients
// to limit the process as a whole.
func WithRateLimiter(l *rate.Limiter) Option {
	return func(c *Client) { c.limiter = l }
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// NewClient returns a client authenticating with apiKey.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "simply-com-operator",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a response from Simply.com with a status code of 400 or more.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("simply.com API: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("simply.com API: HTTP %d: %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether err is a 404 from Simply.com.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsUnauthorized reports whether Simply.com rejected the credentials (401 or 403).
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

// IsClientError reports whether Simply.com refused the request itself (a 4xx other than 429), so
// sending it again unchanged will fail again.
func IsClientError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 &&
		apiErr.StatusCode != http.StatusTooManyRequests
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
	}
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	// Simply.com ignores the user name; the API key alone identifies the account.
	req.SetBasicAuth("api", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Message: errorMessage(data)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("simply.com API: decoding %s %s: %w", method, path, err)
		}
	}
	return nil
}

// errorMessage reads the error text from either error shape Simply.com answers with: its own
// {"error": "..."} or an RFC 7807 problem document.
func errorMessage(data []byte) string {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Title   string `json:"title"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(data, &body) != nil {
		s := strings.TrimSpace(string(data))
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
	for _, s := range []string{body.Error, body.Detail, body.Message, body.Title} {
		if s != "" {
			return s
		}
	}
	return ""
}

// Product is a Simply.com product, which for a domain is named by the domain itself.
type Product struct {
	Object string `json:"object"`
	Name   string `json:"name"`
	Domain struct {
		Name string `json:"name"`
	} `json:"domain"`
}

// ListProducts lists the products the API key can reach. It doubles as a credentials check.
func (c *Client) ListProducts(ctx context.Context) ([]Product, error) {
	var out struct {
		Products []Product `json:"products"`
	}
	if err := c.do(ctx, http.MethodGet, "/my/products/", nil, &out); err != nil {
		return nil, err
	}
	return out.Products, nil
}

// DNSRecord is a record in a Simply.com DNS zone. Name is relative to the zone, "@" for the apex.
type DNSRecord struct {
	ID       int64   `json:"record_id,omitempty"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Data     string  `json:"data"`
	TTL      *int32  `json:"ttl,omitempty"`
	Priority *int32  `json:"priority,omitempty"`
	Comment  *string `json:"comment,omitempty"`
}

func recordsPath(zone string) string {
	return "/my/products/" + url.PathEscape(zone) + "/dns/records/"
}

// ListDNSRecords returns every record in zone. The API has no filter, so this is the whole zone.
func (c *Client) ListDNSRecords(ctx context.Context, zone string) ([]DNSRecord, error) {
	var out struct {
		Records []DNSRecord `json:"records"`
	}
	if err := c.do(ctx, http.MethodGet, recordsPath(zone), nil, &out); err != nil {
		return nil, err
	}
	return out.Records, nil
}

// recordPayload is the body of a create or update. A PUT replaces the whole record, so a field
// left out is cleared: the comment in particular must always be sent.
type recordPayload struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Data     string  `json:"data"`
	TTL      *int32  `json:"ttl,omitempty"`
	Priority *int32  `json:"priority,omitempty"`
	Comment  *string `json:"comment,omitempty"`
}

func payload(r DNSRecord) recordPayload {
	return recordPayload{Name: r.Name, Type: r.Type, Data: r.Data, TTL: r.TTL, Priority: r.Priority, Comment: r.Comment}
}

// CreateDNSRecord adds r to zone and returns the new record's id. Simply.com accepts exact
// duplicates, so callers must check the zone first.
func (c *Client) CreateDNSRecord(ctx context.Context, zone string, r DNSRecord) (int64, error) {
	var out struct {
		Record struct {
			ID int64 `json:"id"`
		} `json:"record"`
	}
	if err := c.do(ctx, http.MethodPost, recordsPath(zone), payload(r), &out); err != nil {
		return 0, err
	}
	if out.Record.ID == 0 {
		return 0, errors.New("simply.com API: create returned no record id")
	}
	return out.Record.ID, nil
}

// UpdateDNSRecord replaces record id in zone with r.
func (c *Client) UpdateDNSRecord(ctx context.Context, zone string, id int64, r DNSRecord) error {
	return c.do(ctx, http.MethodPut, recordsPath(zone)+strconv.FormatInt(id, 10)+"/", payload(r), nil)
}

// DeleteDNSRecord removes record id from zone.
func (c *Client) DeleteDNSRecord(ctx context.Context, zone string, id int64) error {
	return c.do(ctx, http.MethodDelete, recordsPath(zone)+strconv.FormatInt(id, 10)+"/", nil, nil)
}

// ReloadDNSZone asks Simply.com to publish zone. Changes have been seen to stay unpublished until
// this is called, although the API documentation says writes reload the zone themselves.
func (c *Client) ReloadDNSZone(ctx context.Context, zone string) error {
	return c.do(ctx, http.MethodPost, "/my/products/"+url.PathEscape(zone)+"/dns/reload/", nil, nil)
}
