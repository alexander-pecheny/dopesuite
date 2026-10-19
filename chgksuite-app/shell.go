package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"xy/localserver"
)

const localID = "local"

// A tab is one panel: the Local xy or a server the user added.
type Tab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

var localTab = Tab{ID: localID, Title: "Локальный режим", URL: "/"}

// Shell is the service the tab strip calls. It owns the panels, so the strip
// names a tab and never hands a URL to a panel itself.
type Shell struct {
	path   string
	local  *localserver.Server
	window *application.WebviewWindow

	mu      sync.Mutex
	servers []Tab
	panels  map[string]*application.WebviewPanel
	area    application.Rect
}

func openShell(path string, local *localserver.Server) (*Shell, error) {
	s := &Shell{path: path, local: local, panels: map[string]*application.WebviewPanel{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.servers); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Tabs lists Local and then the servers in the order they were added.
func (s *Shell) Tabs() []Tab {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Tab{localTab}, s.servers...)
}

// Select shows the tab with this id and hides the others. An empty id hides
// them all, so the strip's own page shows below it.
func (s *Shell) Select(id string) error {
	return s.show(id, "")
}

// show is Select that also sends the tab's panel to url, when it is not "".
func (s *Shell) show(id, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tab Tab
	if id != "" {
		var ok bool
		if tab, ok = s.tab(id); !ok {
			return fmt.Errorf("no tab %q", id)
		}
	}
	for name, p := range s.panels {
		if name != id {
			p.Hide()
		}
	}
	if id == "" {
		return nil
	}
	p := s.panels[id]
	if p != nil && url != "" {
		p.SetURL(url)
	}
	if p == nil {
		if url != "" {
			tab.URL = url
		}
		hidden := false
		p = s.window.NewPanel(application.WebviewPanelOptions{
			Name: id, URL: tab.URL, Visible: &hidden,
			X: s.area.X, Y: s.area.Y, Width: s.area.Width, Height: s.area.Height,
		})
		s.panels[id] = p
	}
	p.Show()
	return nil
}

// pickFile is the native dialog behind the board's «Импорт из файла…» in the
// Local tab. It is unexported so the strip cannot call it.
func (s *Shell) pickFile() (string, error) {
	return application.Get().Dialog.OpenFile().
		CanChooseFiles(true).
		AddFilter("Пакеты", "*.4s;*.si4s;*.docx;*.txt;*.zip").
		AttachToWindow(s.window).PromptForSingleSelection()
}

// Place sets where the panels go: the strip measures the room below itself.
func (s *Shell) Place(x, y, width, height int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.area = application.Rect{X: x, Y: y, Width: width, Height: height}
	for _, p := range s.panels {
		p.SetBounds(s.area)
	}
}

// Add saves a server tab for the xy at address, which may omit https://.
func (s *Shell) Add(address string) (Tab, error) {
	u, err := serverURL(address)
	if err != nil {
		return Tab{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A removed tab's panel may still be closing, so an id is never reused.
	tab := Tab{ID: "server-" + strconv.FormatInt(time.Now().UnixNano(), 36), Title: u.Host, URL: u.String()}
	s.servers = append(s.servers, tab)
	return tab, s.save()
}

// Remove forgets a server tab. The login stays in the shared profile.
func (s *Shell) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.servers, func(t Tab) bool { return t.ID == id })
	if i < 0 {
		return fmt.Errorf("no server tab %q", id)
	}
	if p := s.panels[id]; p != nil {
		p.Destroy()
		delete(s.panels, id)
	}
	s.servers = slices.Delete(s.servers, i, i+1)
	return s.save()
}

func (s *Shell) tab(id string) (Tab, bool) {
	if id == localID {
		return localTab, true
	}
	i := slices.IndexFunc(s.servers, func(t Tab) bool { return t.ID == id })
	if i < 0 {
		return Tab{}, false
	}
	return s.servers[i], true
}

func (s *Shell) save() error {
	data, err := json.MarshalIndent(s.servers, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// serverURL is the root of the xy at address. It must be https, except on
// this computer, where a development server runs on plain http.
func serverURL(address string) (*url.URL, error) {
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("«%s» не похоже на адрес сервера", address)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback(u.Hostname())) {
		return nil, fmt.Errorf("%s: сервер должен работать по https", u.Host)
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
