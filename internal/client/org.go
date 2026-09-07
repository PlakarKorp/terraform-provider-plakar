// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"fmt"
)

// Organization is the slice of the v1 organization we manage. v1 has no
// organization update route: everything here is set at creation.
type Organization struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	ParentID *string           `json:"parent_id"`
	Info     map[string]string `json:"info"`
}

type OrganizationRequest struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Info     map[string]string `json:"info"`
	ParentID string            `json:"parent_id"`
}

func (c *Client) CreateOrganization(req *OrganizationRequest) (*Organization, error) {
	var out Organization
	if err := c.Do("POST", "/api/v1/account/organizations", req, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetOrganization(id string) (*Organization, error) {
	var out Organization
	if err := c.Do("GET", "/api/v1/account/organizations/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteOrganization(id string) error {
	return c.Do("DELETE", "/api/v1/account/organizations/"+id, nil, nil, nil)
}

// FindOrganization locates one organization by name in the badge
// organization's subtree — the badge organization itself, then its
// descendants, breadth-first. Nil when absent; a duplicate name across the
// subtree is an error, the same rule the Ansible collection applies.
func (c *Client) FindOrganization(name string) (*Organization, error) {
	rootID, err := c.OrgID()
	if err != nil {
		return nil, err
	}
	root, err := c.GetOrganization(rootID)
	if err != nil {
		return nil, err
	}
	var matches []*Organization
	if root.Name == name {
		matches = append(matches, root)
	}
	frontier := []string{rootID}
	for len(frontier) > 0 {
		var next []string
		for _, parent := range frontier {
			var children []*Organization
			if err := c.Do("GET", "/api/v1/account/organizations/"+parent+"/children",
				nil, nil, &children); err != nil {
				return nil, err
			}
			for _, child := range children {
				if child.Name == name {
					matches = append(matches, child)
				}
				next = append(next, child.ID)
			}
		}
		frontier = next
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%d organizations named %q in the subtree", len(matches), name)
	}
}

// --- members ------------------------------------------------------------------

// Member is one row of an organization's members listing. A membership
// carries no permission — what a member may do is a grant.
type Member struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Account   string `json:"account"`
	Name      string `json:"name"`
	IsService bool   `json:"is_service"`
}

func (c *Client) ListMembers(orgID string) ([]Member, error) {
	return paginate[Member](c, "/api/v1/account/organizations/"+orgID+"/members", nil)
}

// GetMember is one member by user id; v1 has no single-member route, so it is
// the listing filtered here. Nil when the user holds no membership.
func (c *Client) GetMember(orgID, userID string) (*Member, error) {
	members, err := c.ListMembers(orgID)
	if err != nil {
		return nil, err
	}
	for i := range members {
		if members[i].UserID == userID {
			return &members[i], nil
		}
	}
	return nil, nil
}

// FindMember locates one member by email (a person) or, when email is empty,
// by name — service accounts only, so a person whose display name collides
// with an automation account can never receive its grants. Nil when absent.
func (c *Client) FindMember(orgID, email, name string) (*Member, error) {
	members, err := c.ListMembers(orgID)
	if err != nil {
		return nil, err
	}
	var matches []*Member
	for i := range members {
		if email != "" && members[i].Email == email {
			matches = append(matches, &members[i])
		}
		if email == "" && members[i].IsService && members[i].Name == name {
			matches = append(matches, &members[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		if email != "" {
			return nil, fmt.Errorf("%d memberships for %q", len(matches), email)
		}
		return nil, fmt.Errorf("%d service accounts named %q", len(matches), name)
	}
}

// MemberInvite is the created membership: the invitation route with
// auto_accept, the admin path. It covers a new person (a one-time generated
// password comes back), an existing account (membership only), and a service
// account (address minted server-side).
type MemberInvite struct {
	UserID            string
	Email             string
	Account           string
	AccountCreated    bool
	GeneratedPassword *string
}

func (c *Client) InviteMember(orgID, email, name string, service bool) (*MemberInvite, error) {
	body := map[string]any{"auto_accept": true, "is_service": service}
	if email != "" {
		body["email"] = email
	}
	if name != "" {
		body["name"] = name
	}
	var res struct {
		Accepted *struct {
			UserID            string  `json:"user_id"`
			Email             string  `json:"email"`
			Account           string  `json:"account"`
			AccountCreated    bool    `json:"account_created"`
			GeneratedPassword *string `json:"generated_password"`
		} `json:"accepted"`
	}
	if err := c.Do("POST", "/api/v1/account/organizations/"+orgID+"/invitations",
		body, nil, &res); err != nil {
		return nil, err
	}
	if res.Accepted == nil {
		return nil, fmt.Errorf("the invitation was not auto-accepted — the server answered without a membership")
	}
	return &MemberInvite{
		UserID:            res.Accepted.UserID,
		Email:             res.Accepted.Email,
		Account:           res.Accepted.Account,
		AccountCreated:    res.Accepted.AccountCreated,
		GeneratedPassword: res.Accepted.GeneratedPassword,
	}, nil
}

// DeleteMember removes the membership, not the person's account.
func (c *Client) DeleteMember(orgID, userID string) error {
	return c.Do("DELETE", "/api/v1/account/organizations/"+orgID+"/members/"+userID,
		nil, nil, nil)
}

// --- grants -------------------------------------------------------------------

// Grant is one (subject, role) pair on an organization.
type Grant struct {
	ID      string `json:"id"`
	Subject struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"subject"`
	Role struct {
		Name string `json:"name"`
	} `json:"role"`
}

func (c *Client) CreateGrant(orgID, subjectID, role string) (*Grant, error) {
	var out Grant
	if err := c.Do("POST", "/api/v1/account/organizations/"+orgID+"/access",
		map[string]string{"subject_type": "user", "subject_id": subjectID, "role_name": role},
		nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetGrant(orgID, grantID string) (*Grant, error) {
	var out Grant
	if err := c.Do("GET", "/api/v1/account/organizations/"+orgID+"/access/"+grantID,
		nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateGrant changes which role the grant confers, keeping its subject.
// Server-side this replaces the row, so the returned grant carries a NEW id;
// the old one is gone.
func (c *Client) UpdateGrant(orgID, grantID, role string) (*Grant, error) {
	var out Grant
	if err := c.Do("PUT", "/api/v1/account/organizations/"+orgID+"/access/"+grantID,
		map[string]string{"role_name": role}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteGrant(orgID, grantID string) error {
	return c.Do("DELETE", "/api/v1/account/organizations/"+orgID+"/access/"+grantID,
		nil, nil, nil)
}
