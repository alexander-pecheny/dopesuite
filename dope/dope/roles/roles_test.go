package roles

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"creator":  Creator,
		" admin ":  Admin,
		"host":     Host,
		"viewer":   "",
		"":         "",
		"CREATOR":  "", // case-sensitive: stored values are lowercase
		"creator ": Creator,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPredicates(t *testing.T) {
	type want struct {
		manage, access, del, edit, assign bool
	}
	cases := map[string]want{
		Creator:  {manage: true, access: true, del: true, edit: true, assign: false},
		Admin:    {manage: true, access: true, del: false, edit: true, assign: true},
		Host:     {manage: false, access: false, del: false, edit: true, assign: true},
		"viewer": {},
		"":       {},
	}
	for role, w := range cases {
		if got := CanManageFest(role); got != w.manage {
			t.Errorf("CanManageFest(%q) = %v, want %v", role, got, w.manage)
		}
		if got := CanManageAccess(role); got != w.access {
			t.Errorf("CanManageAccess(%q) = %v, want %v", role, got, w.access)
		}
		if got := CanDeleteFest(role); got != w.del {
			t.Errorf("CanDeleteFest(%q) = %v, want %v", role, got, w.del)
		}
		if got := CanEditGameTables(role); got != w.edit {
			t.Errorf("CanEditGameTables(%q) = %v, want %v", role, got, w.edit)
		}
		if got := Assignable(role); got != w.assign {
			t.Errorf("Assignable(%q) = %v, want %v", role, got, w.assign)
		}
	}
}

func TestParseBulkLines(t *testing.T) {
	out, err := ParseBulkLines("alice:admin\n\n bob : host \ncarol:remove")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d lines, want 3: %+v", len(out), out)
	}
	if out[0] != (BulkAccessLine{Line: 1, Nickname: "alice", Role: Admin}) {
		t.Errorf("line 0: %+v", out[0])
	}
	if out[1] != (BulkAccessLine{Line: 3, Nickname: "bob", Role: Host}) {
		t.Errorf("line 1: %+v", out[1])
	}
	if out[2] != (BulkAccessLine{Line: 4, Nickname: "carol", Delete: true}) {
		t.Errorf("line 2: %+v", out[2])
	}

	for _, bad := range []string{"noseparator", "alice:viewer", ":admin", "alice:"} {
		if _, err := ParseBulkLines(bad); err == nil {
			t.Errorf("ParseBulkLines(%q) expected error, got nil", bad)
		}
	}
}
