// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// Integrations are global to the deployment, and these tests run against one
// other work depends on: they only install integrations nothing else uses,
// and every one of them must be gone afterwards. The API key needs the
// platform-admin role.

// Three integrations and one pinned to a version: an empty plan, one
// dropped from the configuration uninstalling it alone, an uninstall made by
// hand put back, and an import by id. (The testing framework's state does not
// support for_each, so the list is spelled out; the for_each pattern of the
// example is the same resource repeated.)
func TestAccIntegration_lifecycle(t *testing.T) {
	// The pinned version is read from the catalog while the steps are built,
	// before resource.Test would skip a run without TF_ACC.
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("%s must be set for acceptance tests", resource.EnvTfAcc)
	}
	testAccPreCheck(t)
	pinned := "etcd"
	listed := []string{"ftp", "imap", "webdav"}
	config := func(names []string) string {
		hcl := fmt.Sprintf(`
resource "plakar_integration" "pinned" {
  name    = %q
  version = %q
}
`, pinned, testAccLatestVersion(t, pinned))
		for _, n := range names {
			hcl += fmt.Sprintf(`
resource "plakar_integration" %q {
  name = %q
}
`, n, n)
		}
		return hcl
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckIntegrationsUninstalled(append(listed, pinned)...),
		Steps: []resource.TestStep{
			{
				Config: config(listed),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckIntegrationCurrent("plakar_integration.pinned"),
					testAccCheckIntegrationCurrent("plakar_integration.ftp"),
					testAccCheckIntegrationCurrent("plakar_integration.imap"),
					testAccCheckIntegrationCurrent("plakar_integration.webdav"),
					// Left out, the version is the catalog's highest, kept in state.
					resource.TestCheckResourceAttr("plakar_integration.ftp",
						"version", testAccLatestVersion(t, "ftp")),
					resource.TestCheckResourceAttr("plakar_integration.pinned",
						"version", testAccLatestVersion(t, pinned)),
				),
			},
			{
				Config:   config(listed),
				PlanOnly: true,
			},
			{
				// Dropping one uninstalls that one and nothing else.
				Config: config([]string{"ftp", "webdav"}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_integration.imap",
							plancheck.ResourceActionDestroy),
						plancheck.ExpectResourceAction("plakar_integration.ftp",
							plancheck.ResourceActionNoop),
						plancheck.ExpectResourceAction("plakar_integration.webdav",
							plancheck.ResourceActionNoop),
						plancheck.ExpectResourceAction("plakar_integration.pinned",
							plancheck.ResourceActionNoop),
					},
				},
				Check: testAccCheckIntegrationsUninstalled("imap"),
			},
			{
				// Uninstalled by hand: the next plan installs it again.
				PreConfig: func() {
					installed, err := testAccClient().InstalledIntegrations("ftp")
					for _, it := range installed {
						if err == nil {
							err = testAccClient().UninstallIntegration(it.ID)
						}
					}
					if err != nil {
						t.Fatalf("uninstalling ftp by hand: %v", err)
					}
				},
				Config: config([]string{"ftp", "webdav"}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("plakar_integration.ftp",
							plancheck.ResourceActionCreate),
					},
				},
				Check: testAccCheckIntegrationCurrent("plakar_integration.ftp"),
			},
			{
				ResourceName:      "plakar_integration.pinned",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// An unknown name or version fails before anything is installed, naming the
// value; the server alone would answer a bare 500.
func TestAccIntegration_unknown(t *testing.T) {
	name := "plakar-acc-" + acctest.RandString(8)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckIntegrationsUninstalled("smb"),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "plakar_integration" "unknown" {
  name = %q
}
`, name),
				ExpectError: regexp.MustCompile(`no integration named\s+"` + name + `"`),
			},
			{
				Config: `
resource "plakar_integration" "unknown" {
  name    = "smb"
  version = "v0.0.0-plakar-acc"
}
`,
				ExpectError: regexp.MustCompile(`no version\s+"v0.0.0-plakar-acc"\s+of\s+"smb"`),
			},
		},
	})
}

// testAccLatestVersion is what an unpinned install must pick.
func testAccLatestVersion(t *testing.T, name string) string {
	t.Helper()
	versions, err := testAccClient().CatalogVersions(name)
	if err != nil {
		t.Fatalf("reading the catalog for %s: %v", name, err)
	}
	latest, err := client.LatestVersion(versions)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return latest
}

// testAccCheckIntegrationCurrent checks the server lists the installed id as
// the current version of its name, at the version in state.
func testAccCheckIntegrationCurrent(address string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[address]
		if !ok {
			return fmt.Errorf("%s not in state", address)
		}
		installed, err := testAccClient().InstalledIntegrations(rs.Primary.Attributes["name"])
		if err != nil {
			return err
		}
		for _, it := range installed {
			if it.ID == rs.Primary.ID {
				if !it.IsCurrent || it.Version != rs.Primary.Attributes["version"] {
					return fmt.Errorf("%s: %s is installed but current=%t at %s", address, it.Name, it.IsCurrent, it.Version)
				}
				return nil
			}
		}
		return fmt.Errorf("%s: %s is not installed", address, rs.Primary.ID)
	}
}

// testAccCheckIntegrationsUninstalled checks none of the names is installed,
// and that the integrations the deployment depends on are as they were.
func testAccCheckIntegrationsUninstalled(names ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c := testAccClient()
		for _, name := range names {
			installed, err := c.InstalledIntegrations(name)
			if err != nil {
				return err
			}
			if len(installed) > 0 {
				return fmt.Errorf("%s is still installed", name)
			}
		}
		for _, name := range []string{"s3", "scaleway", "vault"} {
			if _, err := c.IntegrationByName(name); err != nil {
				return fmt.Errorf("an integration the tests must not touch is gone: %w", err)
			}
		}
		return nil
	}
}
