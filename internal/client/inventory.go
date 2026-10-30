// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
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
// identity only, the listing carries no configuration. LastUpdate is the time
// of the last successful sync since the configuration last changed: a sync
// sets it, a configuration update clears it, and the inventory read does not
// carry it at all.
type InventorySummary struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	LastUpdate *string `json:"last_update"`
}

// SyncTimeout bounds one inventory sync, waiting for a running one
// included. A sync of a project holding 18 resources over three regions took
// 3.4 to 5.8 seconds; the margin is for accounts far larger than that.
const SyncTimeout = 5 * time.Minute

// syncBusy is how the server words a sync refused because another one of the
// same inventory is running, e.g. started from the web interface.
const syncBusy = "already in progress"

// SyncInventory asks the Control Plane to discover the inventory's resources
// and waits for the answer. The route is a GET with a side effect, and it
// answers 200 even when the sync fails: the failure is ok false plus the
// server's message, returned here as the error. A sync refused because
// another one of the same inventory is running is asked again until
// SyncTimeout has passed.
func (c *Client) SyncInventory(id string) error {
	deadline := time.Now().Add(c.syncHTTP.Timeout)
	busy := "" // the server's last refusal, while waiting behind a running sync
	for {
		var res struct {
			OK    bool    `json:"ok"`
			Error *string `json:"error"`
		}
		// Each attempt only gets what is left, so the retries end with the
		// deadline too. Nothing left must not read as no timeout at all,
		// which is what a zero http.Client timeout means.
		attempt := *c.syncHTTP
		attempt.Timeout = time.Until(deadline)
		var err error
		if attempt.Timeout > 0 {
			err = c.do(&attempt, "GET", "/api/v1/inventories/"+id+"/sync", nil, nil, &res)
		}
		var netErr net.Error
		if attempt.Timeout <= 0 || errors.As(err, &netErr) && netErr.Timeout() {
			msg := fmt.Sprintf("the sync did not answer within %s; the Control Plane may still be synchronizing the inventory", c.syncHTTP.Timeout)
			if busy != "" {
				msg += "; another sync of it was running: " + busy
			}
			return errors.New(msg)
		}
		if err != nil {
			return err
		}
		if res.OK {
			return nil
		}
		msg := "the Control Plane reported a failed sync without a message"
		if res.Error != nil && *res.Error != "" {
			msg = *res.Error
		}
		if !strings.Contains(msg, syncBusy) || time.Now().Add(c.syncRetry).After(deadline) {
			return errors.New(msg)
		}
		busy = msg
		time.Sleep(c.syncRetry)
	}
}

// FindInventoryByID returns the inventory's row in the organization listing,
// the only place the server reports when it last synchronized; nil when the
// listing does not hold it.
func (c *Client) FindInventoryByID(id string) (*InventorySummary, error) {
	org, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	items, err := paginate[InventorySummary](c,
		"/api/v1/account/organizations/"+org+"/inventories", nil)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	return nil, nil
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
	c.resourceMu.Lock()
	defer c.resourceMu.Unlock()
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
	c.resourceMu.Lock()
	defer c.resourceMu.Unlock()
	return c.Do("POST", "/api/v1/inventories/"+inventoryID+"/resources/"+urnID,
		res, nil, nil)
}

func (c *Client) DeleteInventoryResource(inventoryID, urnID string) error {
	c.resourceMu.Lock()
	defer c.resourceMu.Unlock()
	return c.Do("DELETE", "/api/v1/inventories/"+inventoryID+"/resources/"+urnID,
		nil, nil, nil)
}
