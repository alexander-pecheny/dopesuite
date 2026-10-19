//go:build nowasmtypst

package server

import (
	"errors"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/handout"
)

func newTypstPool() (handout.Typesetter, error) {
	return nil, errors.New("built without the wasm typst, and Config.Typst names no binary")
}
