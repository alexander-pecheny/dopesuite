package chgkimport

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

type offline struct{}

func (offline) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

func TestTxtHandWrittenCP1251(t *testing.T) {
	packet := "Вопрос 1: Что такое snake_case?\nОтвет: Стиль имён.\nАвтор: Иван Иванов\n"
	raw, err := charmap.Windows1251.NewEncoder().String(packet)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse("пакет.txt", []byte(raw), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "пакет" || !strings.Contains(res.Source, `snake\_case`) || !strings.Contains(res.Source, "! Стиль имён.") {
		t.Fatalf("name %q, source:\n%s", res.Name, res.Source)
	}
}

func TestTxtDBExportMatchesChgksuite(t *testing.T) {
	saved := dbClient
	dbClient = &http.Client{Transport: offline{}}
	defer func() { dbClient = saved }()
	raw, err := os.ReadFile("../textparse/testdata/db_cases/synth.txt")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../textparse/testdata/db_cases/synth.canon")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse("synth.txt", raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != string(want) {
		t.Errorf("mismatch:\n%s", firstDiff(string(want), res.Source))
	}
}
