package xyimage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/fixture.*")

// fixture is testdata/fixture.json: the parts of testdata/fixture.xyimage, as
// both this package and web/ts/xyimagefile.ts (xy/jstest) read them.
type fixture struct {
	Recipe   string   `json:"recipe"`
	Images   []string `json:"images"` // base64
	Overlays []string `json:"overlays"`
	Raster   string   `json:"raster"` // base64
}

const fixtureRecipe = `{"v":1,"frame":{"x":0,"y":0,"w":40,"h":20,"bg":"#ffffff","dpi":300},` +
	`"items":[{"kind":"image","src":0,"name":"dot.png","x":-5,"y":2,"w":20,"h":10},` +
	`{"kind":"text","text":"Раздатка","x":4,"y":4,"w":30,"h":10}],` +
	`"bake":{"w":40,"h":20,"bg":"#ffffff","layers":[{"kind":"image","src":0,"ext":"png","x":-5,"y":2,"w":20,"h":10},{"kind":"svg","over":0}]}}`

const fixtureOverlay = `<svg xmlns="http://www.w3.org/2000/svg" width="40mm" height="20mm" viewBox="0 0 40 20">` +
	`<text font-family="Noto Sans" font-size="4.939" fill="#000"><tspan x="4" y="9">Раздатка</tspan></text></svg>`

// A 1×1 transparent PNG; the container never looks inside it.
var dotPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

func fixtureFile() *File {
	return &File{Recipe: []byte(fixtureRecipe), Images: [][]byte{dotPNG}, Overlays: [][]byte{[]byte(fixtureOverlay)}, Raster: dotPNG}
}

func TestFixtureParity(t *testing.T) {
	enc := fixtureFile().Encode()
	if *update {
		fx := fixture{Recipe: fixtureRecipe, Images: []string{base64.StdEncoding.EncodeToString(dotPNG)},
			Overlays: []string{fixtureOverlay}, Raster: base64.StdEncoding.EncodeToString(dotPNG)}
		js, _ := json.MarshalIndent(fx, "", "  ")
		must(t, os.WriteFile("testdata/fixture.json", append(js, '\n'), 0o644))
		must(t, os.WriteFile("testdata/fixture.xyimage", enc, 0o644))
	}
	want, err := os.ReadFile("testdata/fixture.xyimage")
	must(t, err)
	if !bytes.Equal(enc, want) {
		t.Fatal("Encode differs from testdata/fixture.xyimage (run with -update if the format changed on purpose)")
	}
	f, err := Parse(want)
	must(t, err)
	if string(f.Recipe) != fixtureRecipe || len(f.Images) != 1 || len(f.Overlays) != 1 || !bytes.Equal(f.Raster, dotPNG) {
		t.Fatalf("Parse lost a part: %+v", f)
	}
	if f.Bake.W != 40 || len(f.Bake.Layers) != 2 || f.Bake.Layers[0].X != -5 {
		t.Fatalf("bake = %+v", f.Bake)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func withChunk(t string, data []byte) []byte {
	var b bytes.Buffer
	b.Write(fixtureFile().Encode())
	chunk(&b, t, data)
	return b.Bytes()
}

func TestUnknownChunks(t *testing.T) {
	if _, err := Parse(withChunk("thmb", []byte("x"))); err != nil {
		t.Fatalf("an optional chunk must be skipped, got %v", err)
	}
	if _, err := Parse(withChunk("FONT", []byte("x"))); !errors.Is(err, ErrNewer) {
		t.Fatalf("an unknown critical chunk must be refused as newer, got %v", err)
	}
}

func TestRefusals(t *testing.T) {
	good := fixtureFile().Encode()
	newer := append([]byte(nil), good...)
	newer[len(Magic)] = Version + 1
	cases := map[string]struct {
		b    []byte
		want error
	}{
		"plain png":     {dotPNG, ErrNotXyimage},
		"newer framing": {newer, ErrNewer},
		"truncated":     {good[:len(good)-3], ErrCorrupt},
		"no recipe first": {func() []byte {
			var b bytes.Buffer
			b.WriteString(Magic)
			b.WriteByte(Version)
			chunk(&b, TypeImage, dotPNG)
			return b.Bytes()
		}(), ErrCorrupt},
	}
	for name, c := range cases {
		if _, err := Parse(c.b); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

func TestBadLayerRefused(t *testing.T) {
	f := fixtureFile()
	f.Recipe = []byte(strings.Replace(fixtureRecipe, `"over":0`, `"over":3`, 1))
	if _, err := Parse(f.Encode()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("a layer naming a missing overlay must be refused, got %v", err)
	}
}

func TestNativePx(t *testing.T) {
	f, err := Parse(fixtureFile().Encode())
	must(t, err)
	// 40 × 20 mm at chgksuite's 120 px/in.
	if w, h := f.NativePx(); w != 189 || h != 94 {
		t.Fatalf("NativePx = %d×%d", w, h)
	}
}

func TestVectorSVG(t *testing.T) {
	f, err := Parse(fixtureFile().Encode())
	must(t, err)
	svg, err := f.VectorSVG()
	must(t, err)
	s := string(svg)
	img := strings.Index(s, "<image")
	txt := strings.Index(s, "<text")
	if img < 0 || txt < img {
		t.Fatalf("the picture must come before the overlay drawn over it:\n%s", s)
	}
	if strings.Count(s, "<svg") != 1 || !strings.Contains(s, `x="-5.000"`) || !strings.Contains(s, "data:image/png;base64,") {
		t.Fatalf("composed SVG:\n%s", s)
	}
}
