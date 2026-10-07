// SPDX-License-Identifier: Apache-2.0
package main

import "testing"

func TestIsCLICommandKeepsFlagsOnServePath(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want bool
	}{
		{"report", true}, {"doctor", true}, {"serve", false}, {"--listen", false}, {"--scan-timeout=1s", false},
	} {
		if got := isCLICommand(tc.arg); got != tc.want {
			t.Errorf("isCLICommand(%q)=%t, want %t", tc.arg, got, tc.want)
		}
	}
}
