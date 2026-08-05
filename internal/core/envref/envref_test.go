package envref

import "testing"

func TestExpand(t *testing.T) {
	t.Setenv("REEVE_TEST_VALUE", "resolved")
	cases := []struct {
		in, want string
	}{
		{"${env:REEVE_TEST_VALUE}", "resolved"},
		{"literal", "literal"},
		{"${env:REEVE_TEST_MISSING_VALUE}", ""},
		{"${env:REEVE_TEST_VALUE", "${env:REEVE_TEST_VALUE"},                 // unclosed: left as-is
		{"prefix ${env:REEVE_TEST_VALUE}", "prefix ${env:REEVE_TEST_VALUE}"}, // not exact-match: left as-is
	}
	for _, tc := range cases {
		if got := Expand(tc.in); got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
