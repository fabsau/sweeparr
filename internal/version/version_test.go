package version

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{"2.0.1", "2.0.0", true},
		{"v2.1.0", "2.0.9", true},
		{"3.0.0", "2.9.9", true},
		{"2.0.0", "2.0.0", false},
		{"2.0.0", "2.0.1", false},
		{"1.9.9", "2.0.0", false},
		{"2.0.0", "2.0.0-rc.1", true},
		{"2.0.0-rc.2", "2.0.0", false},
		{"invalid", "2.0.0", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
