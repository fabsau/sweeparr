package auth_test

import (
	"testing"

	"github.com/fabsau/sweeparr/internal/auth"
)

func TestExtractClaimsGroups(t *testing.T) {
	g1 := auth.ExtractClaimsGroups(map[string]any{"groups": []any{"users", "IT_Admin"}}, "groups")
	if len(g1) != 2 || g1[1] != "IT_Admin" {
		t.Fatalf("unexpected groups from []any: %v", g1)
	}
	g2 := auth.ExtractClaimsGroups(map[string]any{"roles": "IT_Admin, viewers"}, "roles")
	if len(g2) != 2 || g2[0] != "IT_Admin" {
		t.Fatalf("unexpected groups from csv string: %v", g2)
	}
}
