// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"fmt"
	"net/url"
)

// Field is one connector configuration value, optionally backed by a secret
// provider.
type Field struct {
	Value    string `json:"value"`
	Provider *struct {
		ID string `json:"id"`
	} `json:"provider,omitempty"`
}

// Connector is the slice of the v1 connector we manage. Read returns more
// (resolved inventory, residency, timestamps); none of it is our contract.
type Connector struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Type      string           `json:"type"`
	Protocol  string           `json:"protocol"`
	Fields    map[string]Field `json:"fields"`
	Endpoints []struct {
		Endpoint string `json:"endpoint"`
	} `json:"endpoints"`
	DataClasses []string `json:"data_classes"`
	Environment string   `json:"environment"`
	Temperature string   `json:"temperature"`
	Integration struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"integration"`
	Resource *struct {
		URNID string `json:"urn_id"`
		URN   string `json:"urn"`
	} `json:"resource"`
}

// ConnectorRequest is the create shape — and, v1 being v1, also the update
// shape: update is a full-body POST on the connector id, so callers merge
// their changes over the connector's current state first.
type ConnectorRequest struct {
	Name        string `json:"name"`
	URNID       string `json:"urn_id"`
	Protocol    string `json:"protocol"`
	Integration struct {
		ID string `json:"id"`
	} `json:"integration"`
	Type      string           `json:"type"`
	Fields    map[string]Field `json:"fields"`
	Endpoints []struct {
		Endpoint string `json:"endpoint"`
	} `json:"endpoints"`
	DataClasses []string `json:"data_classes"`
	Environment string   `json:"environment"`
	Temperature string   `json:"temperature,omitempty"`
}

func (c *Client) GetConnector(id string) (*Connector, error) {
	var out Connector
	if err := c.Do("GET", "/api/v1/connectors/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateConnector(req *ConnectorRequest) (*Connector, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	var out Connector
	if err := c.Do("POST", "/api/v1/account/organizations/"+org+"/connectors",
		req, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateConnector sends the full-body POST v1 wants.
func (c *Client) UpdateConnector(id string, req *ConnectorRequest) error {
	return c.Do("POST", "/api/v1/connectors/"+id, req, nil, nil)
}

func (c *Client) DeleteConnector(id string) error {
	return c.Do("DELETE", "/api/v1/connectors/"+id, nil, nil, nil)
}

// InitializeStore creates the underlying kloset store on a store connector.
func (c *Client) InitializeStore(id, compression string) error {
	var body map[string]string
	if compression != "" {
		body = map[string]string{"compression": compression}
	}
	return c.Do("POST", "/api/v1/connectors/"+id+"/stores/create", body, nil, nil)
}

// FindConnector locates one connector of a kind by name; nil when absent.
func (c *Client) FindConnector(kind, name string) (*Connector, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("type", kind)
	var res struct {
		Items []Connector `json:"items"`
	}
	if err := c.Do("GET", "/api/v1/account/organizations/"+org+"/connectors",
		nil, q, &res); err != nil {
		return nil, err
	}
	var matches []*Connector
	for i := range res.Items {
		if res.Items[i].Name == name {
			matches = append(matches, &res.Items[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%d %s connectors named %q", len(matches), kind, name)
	}
}
