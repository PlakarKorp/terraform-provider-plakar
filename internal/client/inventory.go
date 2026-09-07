// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"fmt"
)

// The typed inventory configurations, one per provider the inventory watches.
// Credential-carrying values ride the same {value, provider} shape as
// connector fields; plain settings are bare strings — that split is the
// wire's, not ours.

type AWSInventoryConfig struct {
	CredentialsType string `json:"credentials_type"`
	AccessKey       Field  `json:"access_key"`
	SecretAccessKey Field  `json:"secret_access_key"`
	Region          string `json:"region"`
}

type OVHInventoryConfig struct {
	ApplicationKey    Field  `json:"application_key"`
	ApplicationSecret Field  `json:"application_secret"`
	ConsumerKey       Field  `json:"consumer_key"`
	Endpoint          string `json:"endpoint"`
}

type ScalewayInventoryConfig struct {
	ProjectID Field `json:"scw_project_id"`
	AccessKey Field `json:"scw_access_key"`
	SecretKey Field `json:"scw_secret_key"`
}

type GCPInventoryConfig struct {
	ProjectID          Field `json:"gcp_project_id"`
	ServiceAccountJSON Field `json:"gcp_service_account_json"`
}

type VMWareInventoryConfig struct {
	Server        Field  `json:"vsphere_server"`
	Username      Field  `json:"vsphere_username"`
	Password      Field  `json:"vsphere_password"`
	TLSSkipVerify string `json:"vsphere_tls_skip_verify"`
	TLSCABundle   Field  `json:"vsphere_tls_ca_bundle"`
}

type K8SInventoryConfig struct {
	Kubeconfig Field `json:"k8s_kubeconf"`
}

// InventoryRequest is the create shape — and, v1 being v1, also the update
// shape: update is a full-body POST on the inventory id. Unlike connectors
// the configuration is typed and fully declared here, so no merge over the
// current state is needed.
type InventoryRequest struct {
	Name     string                   `json:"name"`
	Type     string                   `json:"type"`
	AWS      *AWSInventoryConfig      `json:"aws_configuration,omitempty"`
	OVH      *OVHInventoryConfig      `json:"ovh_configuration,omitempty"`
	Scaleway *ScalewayInventoryConfig `json:"scaleway_configuration,omitempty"`
	GCP      *GCPInventoryConfig      `json:"gcp_configuration,omitempty"`
	VMWare   *VMWareInventoryConfig   `json:"vmware_configuration,omitempty"`
	K8S      *K8SInventoryConfig      `json:"k8s_configuration,omitempty"`
}

// Inventory is the slice of the v1 inventory we manage. The read echoes the
// configuration values unmasked, which is what makes drift detection work.
type Inventory struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Type     string                   `json:"type"`
	AWS      *AWSInventoryConfig      `json:"aws_configuration"`
	OVH      *OVHInventoryConfig      `json:"ovh_configuration"`
	Scaleway *ScalewayInventoryConfig `json:"scaleway_configuration"`
	GCP      *GCPInventoryConfig      `json:"gcp_configuration"`
	VMWare   *VMWareInventoryConfig   `json:"vmware_configuration"`
	K8S      *K8SInventoryConfig      `json:"k8s_configuration"`
}

func (c *Client) CreateInventory(req *InventoryRequest) (*Inventory, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	var out Inventory
	if err := c.Do("POST", "/api/v1/account/organizations/"+org+"/inventories",
		req, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetInventory(id string) (*Inventory, error) {
	var out Inventory
	if err := c.Do("GET", "/api/v1/inventories/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateInventory sends the full-body POST v1 wants. The server refuses a
// type change, which the resource surfaces as RequiresReplace.
func (c *Client) UpdateInventory(id string, req *InventoryRequest) error {
	return c.Do("POST", "/api/v1/inventories/"+id, req, nil, nil)
}

func (c *Client) DeleteInventory(id string) error {
	return c.Do("DELETE", "/api/v1/inventories/"+id, nil, nil, nil)
}

// InventorySummary is one row of the organization's inventory listing —
// identity only, the listing carries no configuration.
type InventorySummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// FindInventory locates one inventory by name; nil when absent.
func (c *Client) FindInventory(name string) (*InventorySummary, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	items, err := paginate[InventorySummary](c,
		"/api/v1/account/organizations/"+org+"/inventories", nil)
	if err != nil {
		return nil, err
	}
	var matches []*InventorySummary
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
		return nil, fmt.Errorf("%d inventories named %q", len(matches), name)
	}
}

// --- resources within an inventory -------------------------------------------

// InventoryResourceEndpoint is one address of a declared resource. The kind
// (host, inet4, inet6) is derived server-side from the endpoint itself, so
// only the endpoint travels on writes.
type InventoryResourceEndpoint struct {
	Kind     string `json:"kind,omitempty"`
	Endpoint string `json:"endpoint"`
}

// InventoryResourceRequest is the write body: what a configuration declares.
// The server-owned fields (urn_id, locked) live on the read shape only, so a
// write can never carry them — today the server ignores unknown keys, but a
// body that silently lifted an operator's lock the day it stops would be a
// bad way to find out.
type InventoryResourceRequest struct {
	URN                  string                      `json:"urn"`
	Name                 string                      `json:"name"`
	Class                string                      `json:"class"`
	SubClass             string                      `json:"subclass"`
	Service              string                      `json:"service"`
	Tags                 []string                    `json:"tags"`
	Endpoints            []InventoryResourceEndpoint `json:"endpoints"`
	ExcludedFromCoverage bool                        `json:"excluded_from_coverage"`
}

// InventoryResource is the read shape: the declaration plus what the server
// owns. urn_id is the resource's identity on the write path; the row id is
// not addressable.
type InventoryResource struct {
	InventoryResourceRequest
	URNID  string `json:"urn_id"`
	Locked bool   `json:"locked"`
}

// CreateInventoryResource declares a resource in a self-managed inventory.
// The create response does not echo endpoints; callers wanting the canonical
// state re-read the resource.
func (c *Client) CreateInventoryResource(inventoryID string, res *InventoryResourceRequest) (*InventoryResource, error) {
	var out InventoryResource
	if err := c.Do("POST", "/api/v1/inventories/"+inventoryID+"/resources",
		res, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetInventoryResource(inventoryID, urnID string) (*InventoryResource, error) {
	var out InventoryResource
	if err := c.Do("GET", "/api/v1/inventories/"+inventoryID+"/resources/"+urnID,
		nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateInventoryResource sends the full-body POST v1 wants. The URN is
// immutable server-side, which the resource surfaces as RequiresReplace.
func (c *Client) UpdateInventoryResource(inventoryID, urnID string, res *InventoryResourceRequest) error {
	return c.Do("POST", "/api/v1/inventories/"+inventoryID+"/resources/"+urnID,
		res, nil, nil)
}

func (c *Client) DeleteInventoryResource(inventoryID, urnID string) error {
	return c.Do("DELETE", "/api/v1/inventories/"+inventoryID+"/resources/"+urnID,
		nil, nil, nil)
}
