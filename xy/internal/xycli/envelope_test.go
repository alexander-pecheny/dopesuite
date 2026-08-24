package xycli

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the Go half of the Envelope parity fixture")

const fixturePath = "testdata/envelope.json"

type sealedField struct {
	Plain string `json:"plain"`
	Enc   string `json:"enc"`
}

type envelopeFixture struct {
	Passphrase string        `json:"passphrase"`
	Keymeta    Keymeta       `json:"keymeta"`
	TSSealed   []sealedField `json:"ts_sealed"`
	GoSealed   []sealedField `json:"go_sealed"`
}

// TestEnvelopeParity: the CLI opens what the browser sealed, under a key it
// derived from the same passphrase. The other direction — the browser opening
// what this seals — is jstest/envelope_parity.test.js, over the go_sealed half
// this writes with -update.
func TestEnvelopeParity(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fx envelopeFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}

	dk, err := Unlock(fx.Passphrase, fx.Keymeta)
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}

	for _, f := range fx.TSSealed {
		got, err := dk.DecField(f.Enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", f.Plain, err)
		}
		if got != f.Plain {
			t.Errorf("decrypted %q, want %q", got, f.Plain)
		}
	}

	if len(fx.GoSealed) != len(fx.TSSealed) {
		t.Fatalf("go_sealed has %d fields, ts_sealed %d — rerun with -update, then the jstest half",
			len(fx.GoSealed), len(fx.TSSealed))
	}
	for _, f := range fx.GoSealed {
		got, err := dk.DecField(f.Enc)
		if err != nil || got != f.Plain {
			t.Errorf("go_sealed %q: %q, %v", f.Plain, got, err)
		}
	}

	if *update {
		fx.GoSealed = nil
		for _, f := range fx.TSSealed {
			enc, err := dk.EncField(f.Plain)
			if err != nil {
				t.Fatal(err)
			}
			fx.GoSealed = append(fx.GoSealed, sealedField{Plain: f.Plain, Enc: enc})
		}
		out, err := json.MarshalIndent(fx, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnlockRejectsWrongPassphrase(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fx envelopeFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if _, err := Unlock(fx.Passphrase+" не тот", fx.Keymeta); err != ErrWrongPassphrase {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	dk := DataKey(make([]byte, 32))
	enc, err := dk.EncField("вопрос")
	if err != nil {
		t.Fatal(err)
	}
	got, err := dk.DecField(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != "вопрос" {
		t.Fatalf("round trip = %q", got)
	}
	// A tampered envelope must not open.
	raw, err := b64dec(enc)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0x01
	if _, err := dk.DecField(b64enc(raw)); err == nil {
		t.Fatal("tampered envelope opened")
	}
}
