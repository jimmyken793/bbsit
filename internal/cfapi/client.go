// Package cfapi is a tiny Cloudflare API v4 client for the slice of
// functionality bbsit needs: looking up zones and managing DNS records for
// tunnel CNAMEs. We deliberately avoid pulling in cloudflare-go (heavy, lots
// of unrelated surface area) — bbsit only ever needs four endpoints.
package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api.cloudflare.com/client/v4"

// Client talks to the Cloudflare REST API using a single API token.
// The token must have at minimum: Zone.Zone:Read, Zone.DNS:Edit on the
// relevant zones.
type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

// Zone is a minimal projection of a Cloudflare zone.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DNSRecord is a minimal projection of a Cloudflare DNS record.
type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type apiEnvelope struct {
	Success bool              `json:"success"`
	Errors  []apiErrorEnvelop `json:"errors"`
	Result  json.RawMessage   `json:"result"`
}

type apiErrorEnvelop struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	u := baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode response (status %d): %w: %s", resp.StatusCode, err, truncate(string(raw), 200))
	}
	if !env.Success {
		return fmt.Errorf("cloudflare API: %s", formatErrors(env.Errors))
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
	}
	return nil
}

func formatErrors(errs []apiErrorEnvelop) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = fmt.Sprintf("%d: %s", e.Code, e.Message)
	}
	return strings.Join(parts, "; ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ListZones returns all zones the token can access. The Cloudflare list endpoint
// paginates; we walk pages here so callers can build a suffix-match table once.
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	page := 1
	for {
		q := url.Values{}
		q.Set("per_page", "50")
		q.Set("page", fmt.Sprintf("%d", page))
		var batch []Zone
		if err := c.do(ctx, http.MethodGet, "/zones", q, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 50 {
			break
		}
		page++
		if page > 50 {
			break // safety: never loop forever, 2500 zones is plenty
		}
	}
	return all, nil
}

// FindZoneForHostname returns the zone whose Name is the longest suffix match
// of host (e.g. "marketing.jomican.com" → zone "jomican.com").
func FindZoneForHostname(zones []Zone, host string) *Zone {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	var best *Zone
	for i, z := range zones {
		zn := strings.ToLower(z.Name)
		if host == zn || strings.HasSuffix(host, "."+zn) {
			if best == nil || len(z.Name) > len(best.Name) {
				best = &zones[i]
			}
		}
	}
	return best
}

// ListDNSRecords returns DNS records in a zone matching the given name (FQDN).
func (c *Client) ListDNSRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	q := url.Values{}
	q.Set("name", name)
	var recs []DNSRecord
	if err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records", q, nil, &recs); err != nil {
		return nil, err
	}
	return recs, nil
}

func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, rec DNSRecord) (*DNSRecord, error) {
	var out DNSRecord
	if err := c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", nil, rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, rec DNSRecord) (*DNSRecord, error) {
	var out DNSRecord
	if err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+recordID, nil, rec, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
