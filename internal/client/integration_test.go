// Copyright (c) 2026 PlakarKorp
// ISC License (see LICENSE)

package client

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.1.13", "v1.1.13", 0},
		{"v1.1.13", "v1.1.9", 1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.1.0", "v1.1.0-rc.2", 1},
		{"v1.1.0-rc.2", "v1.1.0-beta.8", 1},
		{"v1.1.0-beta.10", "v1.1.0-beta.9", 1},
		{"v1.1.0-alpha", "v1.1.0-alpha.1", -1},
		{"v1.1.0-alpha.beta", "v1.1.0-alpha.1", 1},
		{"v1.1", "v1.1.0", 0},
		{"1.2.3", "v1.2.3", 0},
	} {
		got := CompareVersions(tc.a, tc.b)
		if got > 0 {
			got = 1
		} else if got < 0 {
			got = -1
		}
		if got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if back := CompareVersions(tc.b, tc.a); (back > 0) != (tc.want < 0) || (back == 0) != (tc.want == 0) {
			t.Errorf("CompareVersions(%q, %q) is not the reverse of its swap", tc.b, tc.a)
		}
	}
}

func TestLatestVersion(t *testing.T) {
	for _, tc := range []struct {
		versions []string
		want     string
	}{
		{[]string{"v1.1.13"}, "v1.1.13"},
		{[]string{"v1.1.0-alpha.4"}, "v1.1.0-alpha.4"},
		{[]string{"v1.1.9", "v1.1.13", "v1.1.10"}, "v1.1.13"},
		{[]string{"v1.2.0-rc.1", "v1.1.13"}, "v1.2.0-rc.1"},
		{[]string{"v1.2.0-rc.1", "v1.2.0"}, "v1.2.0"},
	} {
		got, err := LatestVersion(tc.versions)
		if err != nil || got != tc.want {
			t.Errorf("LatestVersion(%v) = %q, %v; want %q", tc.versions, got, err, tc.want)
		}
	}
	if _, err := LatestVersion(nil); err == nil {
		t.Error("LatestVersion(nil) did not fail")
	}
}
