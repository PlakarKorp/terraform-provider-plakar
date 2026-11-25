// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import (
	"encoding/json"
	"testing"
)

// A filtered field comes back from a read as the server sent it, so an
// update that carries it keeps the stored value; a plain field never gains
// the mark.
func TestFieldFilteredRoundTrip(t *testing.T) {
	var fields map[string]Field
	if err := json.Unmarshal([]byte(`{"passphrase":{"provider":null,"value":"","filtered":true},"root":{"provider":null,"value":"/x"}}`), &fields); err != nil {
		t.Fatal(err)
	}
	if !fields["passphrase"].Filtered || fields["root"].Filtered {
		t.Fatalf("decoded %+v", fields)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back["passphrase"]["filtered"] != true {
		t.Errorf("the filtered mark did not survive: %s", out)
	}
	if _, ok := back["root"]["filtered"]; ok {
		t.Errorf("a plain field gained the mark: %s", out)
	}
}
