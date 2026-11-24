package adminusers

import "testing"

func TestMayTake(t *testing.T) {
	t.Setenv("TEST_ADMIN_USER", "pecheny")
	cases := []struct {
		username, handle string
		want             bool
	}{
		{"ivanov", "ivanov", true},
		{"ivanov", "", true},
		{"pecheny", "pecheny", true},
		{"pecheny", "@Pecheny", true},
		{"Pecheny", "pecheny", true},
		{"pecheny", "someoneelse", false},
		{"pecheny", "", false},
	}
	for _, c := range cases {
		if got := MayTake("TEST_ADMIN_USER", c.username, c.handle); got != c.want {
			t.Errorf("MayTake(%q, %q) = %v, want %v", c.username, c.handle, got, c.want)
		}
	}
}
