package lj

import (
	"context"
	"crypto/md5" //nolint:gosec // the scheme under test is LiveJournal's
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// The oracle is what chgksuite's LjExporter would post, recorded by
// scripts/gen_lj_oracle.py with the XML-RPC half stubbed out.

type stubHost struct{}

func (stubHost) Upload(name string, _ []byte) (string, error) {
	return "https://img.example/" + name, nil
}

func TestParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle []struct {
		Fixture string   `json:"fixture"`
		Variant string   `json:"variant"`
		Posts   [][]Post `json:"posts"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle) == 0 {
		t.Fatal("empty oracle")
	}
	images, err := loadImages()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range oracle {
		t.Run(run.Fixture+"/"+run.Variant, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", run.Fixture+".4s"))
			if err != nil {
				t.Fatal(err)
			}
			o := Options{}
			switch run.Variant {
			case "nospoilers":
				o.NoSpoilers = true
			case "splittours":
				o.SplitTours = true
			case "genimp":
				o.SplitTours, o.GeneralImpressions = true, true
			}
			got, err := Render(fsource.Parse(string(src), "chgk"), images, stubHost{}, o)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(run.Posts) {
				t.Fatalf("%d groups, want %d", len(got), len(run.Posts))
			}
			for i := range got {
				if len(got[i]) != len(run.Posts[i]) {
					t.Fatalf("group %d: %d posts, want %d", i, len(got[i]), len(run.Posts[i]))
				}
				for j := range got[i] {
					if got[i][j].Header != run.Posts[i][j].Header {
						t.Errorf("group %d post %d header:\n got: %q\nwant: %q",
							i, j, got[i][j].Header, run.Posts[i][j].Header)
					}
					if got[i][j].Content != run.Posts[i][j].Content {
						t.Errorf("group %d post %d content:\n got: %q\nwant: %q",
							i, j, got[i][j].Content, run.Posts[i][j].Content)
					}
				}
			}
		})
	}
}

func loadImages() (map[string][]byte, error) {
	out := map[string][]byte{}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = data
	}
	return out, nil
}

// The posting half has no live service to check against, so what is checked is
// the request it builds and the answer it reads.

func TestEncodeCallSortsAndEscapes(t *testing.T) {
	got, err := encodeCall("LJ.XMLRPC.postevent", map[string]any{
		"subject": "A & B",
		"event":   "<b>x</b>",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0"?>` + "\n" +
		`<methodCall><methodName>LJ.XMLRPC.postevent</methodName><params>` +
		`<param><value><struct>` +
		`<member><name>event</name><value><string>&lt;b&gt;x&lt;/b&gt;</string></value></member>` +
		`<member><name>subject</name><value><string>A &amp; B</string></value></member>` +
		`</struct></value></param></params></methodCall>`
	if string(got) != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestDecodeResponse(t *testing.T) {
	fields, err := decodeResponse([]byte(`<?xml version="1.0"?><methodResponse><params><param><value>` +
		`<struct><member><name>itemid</name><value><int>42</int></value></member>` +
		`<member><name>url</name><value><string>https://example.livejournal.com/1.html</string></value></member>` +
		`</struct></value></param></params></methodResponse>`))
	if err != nil {
		t.Fatal(err)
	}
	if fields["itemid"] != "42" || fields["url"] != "https://example.livejournal.com/1.html" {
		t.Errorf("fields = %v", fields)
	}
}

func TestDecodeResponseFault(t *testing.T) {
	_, err := decodeResponse([]byte(`<?xml version="1.0"?><methodResponse><fault><value><struct>` +
		`<member><name>faultCode</name><value><int>100</int></value></member>` +
		`<member><name>faultString</name><value><string>Invalid password</string></value></member>` +
		`</struct></value></fault></methodResponse>`))
	if err == nil || !strings.Contains(err.Error(), "Invalid password") {
		t.Errorf("err = %v", err)
	}
}

func TestChallengeResponseIsTheDocumentedHash(t *testing.T) {
	// md5(challenge + md5(password)), which is what LiveJournal's own docs and
	// lj.py's get_chal compute.
	c := NewClient(Account{Login: "u", Password: "secret"})
	c.Pause = 0
	pw := md5.Sum([]byte("secret"))
	want := md5.Sum([]byte("chal" + hex.EncodeToString(pw[:])))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<methodResponse><params><param><value><struct>`+
			`<member><name>challenge</name><value><string>chal</string></value></member>`+
			`</struct></value></param></params></methodResponse>`)
	}))
	defer srv.Close()
	c.endpoint = srv.URL
	_, got, err := c.challenge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("response = %s, want %s", got, hex.EncodeToString(want[:]))
	}
}
