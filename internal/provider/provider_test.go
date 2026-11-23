// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"os"

	"github.com/PlakarKorp/terraform-provider-plakar/internal/client"
)

// testAccClient talks to the API directly, for checks on what the server holds.
func testAccClient() *client.Client {
	return client.New(os.Getenv("PLAKAR_API_URL"), os.Getenv("PLAKAR_API_KEY"), "")
}
