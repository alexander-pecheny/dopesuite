package multifest

import "testing"

func TestAVenueLabelNamesItsRepresentativeOnce(t *testing.T) {
	cases := []struct {
		venue Venue
		want  string
	}{
		{Venue{Title: "Санкт-Петербург", Representative: "Лепичев Сергей"}, "Санкт-Петербург / Лепичев Сергей"},
		{Venue{Title: "Москва / Колесов Артём", Representative: "Колесов Артём"}, "Москва / Колесов Артём"},
		{Venue{Title: "Минск"}, "Минск"},
	}
	for _, c := range cases {
		if got := c.venue.Label(); got != c.want {
			t.Errorf("%q is labelled %q, want %q", c.venue.Title, got, c.want)
		}
	}
}
