// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// A 403 on members or grants says which role to ask for; other errors read
// as they are.
func TestWithRoleHint(t *testing.T) {
	forbidden := &client.Error{Method: "GET", Path: "/api/v1/account/organizations/org/members", Status: 403, Detail: "Forbidden"}
	got := withRoleHint(forbidden)
	for _, want := range []string{"GET /api/v1/account/organizations/org/members answered 403: Forbidden", "managing members and grants, reading them included", "administrator or owner role"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not say %q", got, want)
		}
	}
	other := errors.New("connection refused")
	if got := withRoleHint(other); got != "connection refused" {
		t.Errorf("a non-403 error became %q", got)
	}
}
