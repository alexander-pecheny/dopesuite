// Package imghost turns a picture a package refers to by filename into a URL,
// which is what the markdown, base and openquiz exports need and the .docx and
// .pdf ones do not: those embed the bytes, these publish text.
package imghost

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Host uploads a picture and returns the URL it can be read at. name is the
// file's own name, which chgksuite sends along as the title.
type Host interface {
	Upload(name string, data []byte) (string, error)
}

// ImgurClientID is chgksuite's own registered client, which --imgur_client_id
// overrides.
const ImgurClientID = "e86275b3316c6d6"

// Imgur is composer_common.Imgur: an upload with a local cache, so a package
// exported twice does not upload its pictures twice. The cache is keyed by the
// bytes, not the name.
type Imgur struct {
	ClientID string
	// CachePath is where the name→URL map lives; "" is ~/.chgksuite/image_cache.json.
	CachePath string
	// HTTP is the client the upload goes over; nil is one with a timeout.
	HTTP *http.Client
	// Retries is how many times a non-200 is tried again, chgksuite's ten.
	Retries int
	// Wait is the pause between tries, chgksuite's five seconds.
	Wait time.Duration
}

// NewImgur is Imgur with chgksuite's defaults.
func NewImgur(clientID string) *Imgur {
	if clientID == "" {
		clientID = ImgurClientID
	}
	return &Imgur{
		ClientID: clientID,
		HTTP:     &http.Client{Timeout: 2 * time.Minute},
		Retries:  10,
		Wait:     5 * time.Second,
	}
}

func (im *Imgur) Upload(name string, data []byte) (string, error) {
	encoded := []byte(base64.StdEncoding.EncodeToString(data))
	sum := sha256.Sum256(encoded)
	key := hex.EncodeToString(sum[:])

	cache := im.load()
	if url, ok := cache[key]; ok {
		return url, nil
	}
	url, err := im.post(name, string(encoded))
	if err != nil {
		return "", err
	}
	cache[key] = url
	im.save(cache)
	return url, nil
}

func (im *Imgur) post(title, image string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"album_id": nil, "image": image, "title": title, "description": nil,
	})
	if err != nil {
		return "", err
	}
	var last error
	for try := 0; try <= im.Retries; try++ {
		if try > 0 {
			time.Sleep(im.Wait)
		}
		req, err := http.NewRequest(http.MethodPost, "https://api.imgur.com/3/image", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Client-ID "+im.ClientID)
		resp, err := im.client().Do(req)
		if err != nil {
			last = err
			continue
		}
		var parsed struct {
			Data struct {
				Link string `json:"link"`
			} `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			last = fmt.Errorf("imgur: %s", resp.Status)
			continue
		}
		if err != nil {
			return "", err
		}
		return parsed.Data.Link, nil
	}
	return "", last
}

func (im *Imgur) client() *http.Client {
	if im.HTTP == nil {
		return http.DefaultClient
	}
	return im.HTTP
}

func (im *Imgur) cachePath() string {
	if im.CachePath != "" {
		return im.CachePath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".chgksuite", "image_cache.json")
}

func (im *Imgur) load() map[string]string {
	cache := map[string]string{}
	data, err := os.ReadFile(im.cachePath())
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(data, &cache)
	return cache
}

func (im *Imgur) save(cache map[string]string) {
	path := im.cachePath()
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o600)
}
