// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// The framework only calls ValidateConfig on a resource that announces it, so
// the hook silently never runs if this assertion stops holding.
var _ resource.ResourceWithValidateConfig = &connectorResource{}
