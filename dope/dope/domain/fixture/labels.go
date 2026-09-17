package fixture

import dopestrings "dope/i18nstrings"

// stickerLabels reads the sticker names from the Catalog, which is where the
// creation form reads them too (root ADR-0006).
type labels struct{ neutral, noWrong, emptyWrong string }

func stickerLabels() labels {
	s := dopestrings.Default.Host.Games
	return labels{
		neutral:    s.StickerNeutral(),
		noWrong:    s.StickerNowrong(),
		emptyWrong: s.StickerEmptywrong(),
	}
}

// stickerGameLabel is what the creation form calls the stickers variant.
func stickerGameLabel() string { return dopestrings.Default.Host.Games.TypeKsiStickers() }
