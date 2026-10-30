// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"fmt"
	"net/url"
)

// SecretProvider is the slice of the v1 secret provider we manage. Read
// echoes every field unmasked, the credential included, so whatever holds one
// of these must keep its fields out of Terraform state.
type SecretProvider struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Protocol    string           `json:"protocol"`
	Fields      map[string]Field `json:"fields"`
	Integration struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"integration"`
}

// SecretProviderRequest is the create shape, and also the update shape. The
// update is a PUT that replaces the whole field map: a field left out is
// deleted, the credential with it, so callers merge their changes over the
// provider's current state first.
type SecretProviderRequest struct {
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Integration struct {
		ID string `json:"id"`
	} `json:"integration"`
	Fields map[string]Field `json:"fields"`
}

func (c *Client) GetSecretProvider(id string) (*SecretProvider, error) {
	var out SecretProvider
	if err := c.Do("GET", "/api/v1/secret_providers/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateSecretProvider(req *SecretProviderRequest) (*SecretProvider, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	var out SecretProvider
	if err := c.Do("POST", "/api/v1/account/organizations/"+org+"/secret_providers",
		req, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSecretProvider sends the full-field-map PUT v1 wants.
func (c *Client) UpdateSecretProvider(id string, req *SecretProviderRequest) error {
	return c.Do("PUT", "/api/v1/secret_providers/"+id, req, nil, nil)
}

func (c *Client) DeleteSecretProvider(id string) error {
	return c.Do("DELETE", "/api/v1/secret_providers/"+id, nil, nil, nil)
}

// TestSecretProvider asks the server to reach the secret manager with the
// stored configuration. A failed check is not a transport error: it comes
// back as a nil error and the server's reason.
func (c *Client) TestSecretProvider(id string) (ok bool, reason string, err error) {
	var res struct {
		OK    bool    `json:"ok"`
		Error *string `json:"error"`
	}
	if err := c.Do("GET", "/api/v1/secret_providers/"+id+"/test", nil, nil, &res); err != nil {
		return false, "", err
	}
	if res.Error != nil {
		reason = *res.Error
	}
	return res.OK, reason, nil
}

// SecretProviderSummary is one row of the organization's secret provider
// listing, identity only. The listing carries the fields too, credentials
// included; they are left out here so a lookup never holds one it does not
// need. Its protocol comes back empty, so that is read with GetSecretProvider.
type SecretProviderSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FindSecretProvider locates one secret provider by name; nil when absent.
func (c *Client) FindSecretProvider(name string) (*SecretProviderSummary, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	items, err := paginate[SecretProviderSummary](c,
		"/api/v1/account/organizations/"+org+"/secret_providers", nil)
	if err != nil {
		return nil, err
	}
	var matches []*SecretProviderSummary
	for i := range items {
		if items[i].Name == name {
			matches = append(matches, &items[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%d secret providers named %q", len(matches), name)
	}
}

// IntegrationProtocol is one protocol an installed integration declares for a
// kind of use, with the configuration keys it takes. Sensitive marks the keys
// that carry a credential.
type IntegrationProtocol struct {
	Scheme string `json:"scheme"`
	Fields []struct {
		Name      string `json:"name"`
		Required  bool   `json:"required"`
		Sensitive bool   `json:"sensitive"`
	} `json:"fields"`
}

// IntegrationProtocols lists the protocols an installed integration declares
// for one kind of use: store, source, destination or secret_provider. An
// integration that does not serve that use answers 404. Unlike stores, a
// secret provider's protocol does not share its integration's name (vault
// speaks vault-sm), and the server answers an unknown protocol with a 500, so
// callers pick from this list instead of guessing.
func (c *Client) IntegrationProtocols(integrationID, kind string) ([]IntegrationProtocol, error) {
	q := url.Values{}
	q.Set("type", kind)
	var res struct {
		Protocols []IntegrationProtocol `json:"protocols"`
	}
	if err := c.Do("GET", "/api/v1/integrations/"+integrationID, nil, q, &res); err != nil {
		return nil, err
	}
	return res.Protocols, nil
}
