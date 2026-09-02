// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

// Package client is the one place that knows the v1 wire. Resources speak in
// names and attributes; everything v1-shaped — the apikey login, badge expiry,
// org re-scoping, full-body POST updates — is confined here so the v2 swap
// touches only this package.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Client struct {
	APIURL         string
	APIKey         string
	OrganizationID string

	http *http.Client

	mu     sync.Mutex
	badges map[string]string // org key ("" = the key's own org) -> token
	orgID  string            // resolved via /account/me when not configured

	// taskMu serializes scheduler-task mutations; see schedule.go.
	taskMu sync.Mutex
}

func New(apiURL, apiKey, organizationID string) *Client {
	return &Client{
		APIURL:         apiURL,
		APIKey:         apiKey,
		OrganizationID: organizationID,
		http:           &http.Client{Timeout: 30 * time.Second},
		badges:         map[string]string{},
	}
}

// Error carries the API's problem shape ({title, status, detail}).
type Error struct {
	Method string
	Path   string
	Status int
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s answered %d: %s", e.Method, e.Path, e.Status, e.Detail)
}

// IsNotFound reports a 404, the signal Read uses to drop a resource from state.
func IsNotFound(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// --- auth -------------------------------------------------------------------

func (c *Client) login() (string, error) {
	var res struct {
		Token string `json:"token"`
	}
	if err := c.raw("POST", "/api/v1/auth/login/apikey", "",
		map[string]string{"api_key": c.APIKey}, nil, &res); err != nil {
		return "", fmt.Errorf("api key login failed: %w", err)
	}
	return res.Token, nil
}

func (c *Client) token(refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	org := c.OrganizationID
	if refresh || c.badges[org] == "" {
		base := c.badges[""]
		if refresh || base == "" {
			var err error
			if base, err = c.login(); err != nil {
				return "", err
			}
			c.badges[""] = base
		}
		if org != "" {
			var res struct {
				Token string `json:"token"`
			}
			if err := c.raw("POST", "/api/v1/auth/badge", base,
				map[string]string{"organization_id": org}, nil, &res); err != nil {
				return "", fmt.Errorf("badge exchange for organization %s failed: %w", org, err)
			}
			c.badges[org] = res.Token
		}
	}
	return c.badges[org], nil
}

// --- transport --------------------------------------------------------------

func (c *Client) raw(method, path, token string, body, query any, out any) error {
	u := c.APIURL + path
	if q, ok := query.(url.Values); ok && len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail := ""
		var problem struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data, &problem) == nil {
			detail = problem.Detail
		}
		if detail == "" {
			detail = string(data)
		}
		return &Error{Method: method, Path: path, Status: resp.StatusCode, Detail: detail}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Do performs an authenticated request with one transparent re-login on 401.
func (c *Client) Do(method, path string, body any, query url.Values, out any) error {
	tok, err := c.token(false)
	if err != nil {
		return err
	}
	err = c.raw(method, path, tok, body, query, out)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
		if tok, err = c.token(true); err != nil {
			return err
		}
		err = c.raw(method, path, tok, body, query, out)
	}
	return err
}

// --- org / lookups ----------------------------------------------------------

func (c *Client) OrgID() (string, error) {
	if c.OrganizationID != "" {
		return c.OrganizationID, nil
	}
	c.mu.Lock()
	cached := c.orgID
	c.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	// v1 only hands the badge's organization back through /account/me.
	var me struct {
		OrganizationID string `json:"organization_id"`
	}
	if err := c.Do("GET", "/api/v1/account/me", nil, nil, &me); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.orgID = me.OrganizationID
	c.mu.Unlock()
	return me.OrganizationID, nil
}

// Integration is the slice of the installed-integration record we need.
type Integration struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) IntegrationByName(name string) (*Integration, error) {
	var res struct {
		Items []Integration `json:"items"`
	}
	if err := c.Do("GET", "/api/v1/integrations/installed", nil, nil, &res); err != nil {
		return nil, err
	}
	for i := range res.Items {
		if res.Items[i].Name == name {
			return &res.Items[i], nil
		}
	}
	names := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		names = append(names, it.Name)
	}
	return nil, fmt.Errorf("no installed integration named %q — installed: %v", name, names)
}

// Resource is the slice of an inventory resource we need.
type Resource struct {
	URNID string `json:"urn_id"`
	URN   string `json:"urn"`
	Name  string `json:"name"`
}

// ResourceRef finds a resource across all inventories, by URN first, then by
// name. The route serves at most 50 rows per page whatever limit is asked;
// the search filter narrows server-side, exact matching happens here.
func (c *Client) ResourceRef(value string) (*Resource, error) {
	var items []Resource
	offset := 0
	for {
		q := url.Values{}
		q.Set("limit", "50")
		q.Set("offset", fmt.Sprint(offset))
		q.Set("search", value)
		var page struct {
			Total int        `json:"total"`
			Items []Resource `json:"items"`
		}
		if err := c.Do("GET", "/api/v1/inventories/resources", nil, q, &page); err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total {
			break
		}
	}
	for i := range items {
		if items[i].URN == value {
			return &items[i], nil
		}
	}
	var byName []*Resource
	for i := range items {
		if items[i].Name == value {
			byName = append(byName, &items[i])
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return nil, fmt.Errorf("no resource with URN or name %q", value)
	default:
		return nil, fmt.Errorf("%d resources named %q — use the URN to disambiguate", len(byName), value)
	}
}
