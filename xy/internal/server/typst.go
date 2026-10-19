package server

import (
	"log"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
)

// typesetter returns the shared typesetter, building it on first use. Main also
// warms it in the background at boot, so a user rarely waits for it.
//
// A server runs typst in-process as wasm (typstpool.go), so handout rendering
// never hands the user's decrypted questions to a filesystem. The desktop app
// names a typst binary in Config.Typst instead: its boards are plaintext files
// on the user's own disk already, and the binary is faster and smaller.
func (s *server) typesetter() (handout.Typesetter, error) {
	s.typstOnce.Do(func() {
		if s.typst != nil {
			return // injected (tests)
		}
		if s.cfg.Typst != "" {
			s.typst, s.typstErr = handout.NewCLITypesetterWith(handout.CLIOptions{Bin: s.cfg.Typst, SystemFonts: true})
			return
		}
		s.typst, s.typstErr = newTypstPool()
	})
	if s.typstErr != nil {
		return nil, s.typstErr
	}
	return s.typst, nil
}

// warmTypst builds the typesetter ahead of the first request. Cold, the wasm is
// a ~15s compile (a fresh host, or after a wazero upgrade invalidates the
// cache), which no user should have to sit through.
func (s *server) warmTypst() {
	go func() {
		if _, err := s.typesetter(); err != nil {
			log.Printf("typst unavailable: %v", err)
		}
	}()
}
