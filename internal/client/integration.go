// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// InstalledIntegration is one row of the installed-integration listing. Its
// id is the package's: installing the same version again, even after an
// uninstall, gives the same id back.
type InstalledIntegration struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	IsCurrent bool   `json:"is_current"`
}

// CatalogVersions lists the versions the catalog offers for an integration.
// Empty when the name is unknown: the install route answers an unknown name
// or version with a bare 500, so callers check here first.
func (c *Client) CatalogVersions(name string) ([]string, error) {
	items, err := paginate[struct {
		Version string `json:"version"`
	}](c, "/api/v1/integrations/catalog/"+url.PathEscape(name)+"/versions", nil)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(items))
	for _, it := range items {
		versions = append(versions, it.Version)
	}
	return versions, nil
}

// InstalledIntegrations lists the installed versions of one integration. An
// uninstalled version leaves this listing, though a GET by its id still
// answers, with is_current false: this listing is the one to trust.
func (c *Client) InstalledIntegrations(name string) ([]InstalledIntegration, error) {
	q := url.Values{}
	q.Set("name", name)
	return paginate[InstalledIntegration](c, "/api/v1/integrations/installed", q)
}

func (c *Client) GetInstalledIntegration(id string) (*InstalledIntegration, error) {
	var out InstalledIntegration
	if err := c.Do("GET", "/api/v1/integrations/installed/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InstallIntegration installs a version and makes it the current one, which
// is what installing from the UI does. Installing requires write on the
// deployment itself (the platform-admin or superuser role), not only on the
// organization.
func (c *Client) InstallIntegration(name, version string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Do("POST", "/api/v1/integrations/installed/current",
		map[string]string{"name": name, "version": version}, nil, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// UninstallIntegration answers 200 even for a version already uninstalled.
func (c *Client) UninstallIntegration(id string) error {
	return c.Do("DELETE", "/api/v1/integrations/installed/"+id, nil, nil, nil)
}

// LatestVersion picks the highest version in semantic-version order. The
// catalog marks no version as stable or latest, so a prerelease is chosen
// when nothing newer is final.
func LatestVersion(versions []string) (string, error) {
	if len(versions) == 0 {
		return "", fmt.Errorf("no version to choose from")
	}
	latest := versions[0]
	for _, v := range versions[1:] {
		if CompareVersions(v, latest) > 0 {
			latest = v
		}
	}
	return latest, nil
}

// CompareVersions orders two "v1.2.3[-pre]" versions the semver way: numeric
// parts first, then a final release after any of its prereleases, then
// prerelease identifiers one by one, numeric ones numerically.
func CompareVersions(a, b string) int {
	aCore, aPre, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	bCore, bPre, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	if c := compareIdentifiers(strings.Split(aCore, "."), strings.Split(bCore, "."), true); c != 0 {
		return c
	}
	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	}
	return compareIdentifiers(strings.Split(aPre, "."), strings.Split(bPre, "."), false)
}

// compareIdentifiers compares dot-separated parts. In the version core a
// missing part counts as zero; among prerelease identifiers the shorter list
// is lower when all shared parts are equal.
func compareIdentifiers(a, b []string, core bool) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		if !core && (i >= len(a) || i >= len(b)) {
			return len(a) - len(b)
		}
		if c := compareIdentifier(part(a, i), part(b, i)); c != 0 {
			return c
		}
	}
	return 0
}

// part is the i-th identifier, empty past the end.
func part(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

// compareIdentifier compares numbers numerically, and before any
// alphanumeric identifier; an empty part is a zero.
func compareIdentifier(a, b string) int {
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
