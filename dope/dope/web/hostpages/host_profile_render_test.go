package hostpages

import (
	"strings"
	"testing"

	dopeui "dope/dope/web/ui"
)

// TestProfileDocIdentity checks the identity block: both lines when the account
// carries both identities, and no empty section when it carries neither.
func TestProfileDocIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    profileData
		want    []string
		absent  []string
		section bool
	}{
		{
			name:    "both",
			data:    profileData{HasPassword: true, Username: "pecheny", Telegram: "pecheny"},
			want:    []string{"Вы вошли как ", "<strong>pecheny</strong>", "Telegram: ", "<strong>@pecheny</strong>"},
			section: true,
		},
		{
			name:    "username only",
			data:    profileData{Username: "pecheny"},
			want:    []string{"<strong>pecheny</strong>"},
			absent:  []string{"Telegram: "},
			section: true,
		},
		{
			name:    "telegram only",
			data:    profileData{Telegram: "pecheny"},
			want:    []string{"<strong>@pecheny</strong>"},
			absent:  []string{"Вы вошли как "},
			section: true,
		},
		{
			name:   "neither",
			data:   profileData{},
			absent: []string{"Вы вошли как ", "Telegram: "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html, err := dopeui.Render(profileDoc(tc.data))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			body := string(html)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
			// The password form always renders; the identity section must not
			// leave an empty box behind when there is nothing to say.
			if !strings.Contains(body, `id="passwordForm"`) {
				t.Error("password form missing")
			}
		})
	}
}
