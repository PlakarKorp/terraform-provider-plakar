// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// A URN and a name for one inventory resource are the same resource; anything
// else, or anything that cannot be told, is a move.
func TestMovesResource(t *testing.T) {
	const urn, name, urnID = "urn:lab:bucket/a", "Bucket A", "id-a"
	resolve := func(value string) (*client.Resource, error) {
		switch value {
		case urn, name:
			return &client.Resource{URNID: urnID, URN: urn, Name: name}, nil
		case "Bucket B":
			return &client.Resource{URNID: "id-b", URN: "urn:lab:bucket/b", Name: "Bucket B"}, nil
		}
		return nil, errors.New("no resource with URN or name " + value)
	}
	str := types.StringValue
	for _, tc := range []struct {
		desc               string
		state, plan, urnID types.String
		lookups            int
		want               bool
	}{
		{"unchanged", str(urn), str(urn), str(urnID), 0, false},
		{"URN in state, name planned", str(urn), str(name), str(urnID), 1, false},
		{"name in state, URN planned", str(name), str(urn), str(urnID), 1, false},
		{"another resource", str(urn), str("Bucket B"), str(urnID), 1, true},
		{"a name that does not resolve", str(urn), str("Bucket C"), str(urnID), 1, true},
		{"not known yet", str(urn), types.StringUnknown(), str(urnID), 0, true},
		{"no resource in state", types.StringNull(), str(name), types.StringNull(), 0, true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			lookups := 0
			counted := func(value string) (*client.Resource, error) {
				lookups++
				return resolve(value)
			}
			if got := movesResource(counted, tc.state, tc.plan, tc.urnID); got != tc.want {
				t.Errorf("movesResource = %v, want %v", got, tc.want)
			}
			if lookups != tc.lookups {
				t.Errorf("%d lookups, want %d", lookups, tc.lookups)
			}
		})
	}
}

// A connector named by the name of its resource, then by its URN: the same
// resource, updated in place. Moved to another resource, it is replaced.
func TestAccConnector_resourceByNameOrURN(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test, set TF_ACC")
	}
	testAccPreCheck(t)
	res, err := testAccClient().ResourceRef("Novel Core")
	if err != nil {
		t.Fatalf("resolving resource: %v", err)
	}
	name := "TF acceptance source by name " + acctest.RandString(8)
	config := func(ref string) string {
		return fmt.Sprintf(`
resource "plakar_connector" "lab" {
  name        = %q
  type        = "source"
  integration = "s3"
  resource    = %q
  fields = {
    root = "/source-by-name"
  }
}
`, name, ref)
	}
	action := func(want plancheck.ResourceActionType) resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction("plakar_connector.lab", want),
		}}
	}
	var id string
	keepID := func(want bool) resource.TestCheckFunc {
		return resource.TestCheckResourceAttrWith("plakar_connector.lab", "id", func(got string) error {
			if same := got == id; id != "" && same != want {
				return fmt.Errorf("connector id %s, was %s: kept = %v, want %v", got, id, same, want)
			}
			id = got
			return nil
		})
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckDestroyed,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("Novel Core"),
				Check: resource.ComposeAggregateTestCheckFunc(keepID(true),
					resource.TestCheckResourceAttr("plakar_connector.lab", "urn_id", res.URNID)),
			},
			{
				Config:           config(res.URN),
				ConfigPlanChecks: action(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(keepID(true),
					resource.TestCheckResourceAttr("plakar_connector.lab", "resource", res.URN),
					resource.TestCheckResourceAttr("plakar_connector.lab", "urn_id", res.URNID)),
			},
			{
				Config:           config("Novel Core"),
				ConfigPlanChecks: action(plancheck.ResourceActionUpdate),
				Check:            keepID(true),
			},
			{
				Config:           config("Silent Frost"),
				ConfigPlanChecks: action(plancheck.ResourceActionDestroyBeforeCreate),
				Check:            keepID(false),
			},
		},
	})
}
