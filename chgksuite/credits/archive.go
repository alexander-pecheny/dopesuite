package credits

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// errTooLarge stops reading an archive whose documents inflate past maxInflated.
var errTooLarge = errors.New("archive too large")

// member is one file of an archive. read gives its bytes, within the budget.
type member struct {
	name      string
	encrypted bool
	read      func(budget *int64) ([]byte, error)
}

// readArchive reads the packet out of a .zip, .rar or .7z. A member that can't be
// unpacked or parsed is skipped: the packet is often another file beside it.
func readArchive(ext string, data []byte, opt Options) (*Credits, error) {
	var (
		files                  []file
		candidates, locked, ok int
		err                    error
	)
	budget := int64(maxInflated)
	visit := func(m member) error {
		if !mayBePacket(m.name) {
			return nil
		}
		candidates++
		if m.encrypted {
			locked++
			return nil
		}
		body, err := m.read(&budget)
		if errors.Is(err, errTooLarge) {
			return err
		}
		if errors.Is(err, ErrEncrypted) {
			locked++
			return nil
		}
		if err != nil {
			return nil
		}
		f, err := readOne(path.Base(m.name), body, opt)
		if errors.Is(err, ErrEncrypted) {
			locked++
		}
		if err != nil {
			return nil
		}
		ok++
		if f.questions > 0 {
			f.name = m.name
			files = append(files, f)
		}
		return nil
	}
	switch ext {
	case ".zip":
		err = walkZip(data, visit)
	case ".rar":
		err = walkRar(data, visit)
	default:
		err = walk7z(data, visit)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case len(files) > 0:
		return merge(pick(files, opt.Questions)), nil
	case candidates > 0 && locked == candidates:
		return nil, fmt.Errorf("%w: the archive's documents are password-protected", ErrEncrypted)
	case candidates > 0 && ok == 0:
		return nil, fmt.Errorf("no packet in the archive: no document in it could be read")
	}
	// Handouts, audio, pictures, or documents without questions: not a packet.
	return &Credits{Tours: []Tour{}, Paragraphs: []Paragraph{}, Authors: []Author{}, Sources: []string{}}, nil
}

// mayBePacket skips the members of an archive that cannot be a packet:
// directories, hidden and lock files, macOS metadata and other formats.
func mayBePacket(name string) bool {
	base := path.Base(name)
	if strings.HasSuffix(name, "/") || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "~$") ||
		strings.HasPrefix(name, "__MACOSX/") {
		return false
	}
	return slices.Contains(documents, strings.ToLower(path.Ext(base)))
}

func walkZip(data []byte, visit func(member) error) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("read zip: %w", err)
	}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		err := visit(member{
			name:      zf.Name,
			encrypted: zf.Flags&0x1 != 0,
			read: func(budget *int64) ([]byte, error) {
				rc, err := zf.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return readLimited(rc, budget)
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// walkRar reads a .rar in order: a solid archive can only be unpacked that way.
func walkRar(data []byte, visit func(member) error) error {
	rr, err := rardecode.NewReader(bytes.NewReader(data))
	if err != nil {
		return rarError(err)
	}
	for {
		h, err := rr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return rarError(err)
		}
		if h.IsDir {
			continue
		}
		err = visit(member{
			name:      h.Name,
			encrypted: h.Encrypted,
			read: func(budget *int64) ([]byte, error) {
				body, err := readLimited(rr, budget)
				return body, rarError(err)
			},
		})
		if err != nil {
			return err
		}
	}
}

func rarError(err error) error {
	if errors.Is(err, rardecode.ErrArchiveEncrypted) || errors.Is(err, rardecode.ErrArchivedFileEncrypted) {
		return fmt.Errorf("%w: %v", ErrEncrypted, err)
	}
	if err != nil && !errors.Is(err, errTooLarge) {
		return fmt.Errorf("read rar: %w", err)
	}
	return err
}

func walk7z(data []byte, visit func(member) error) error {
	zr, err := sevenzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return sevenzipError(err)
	}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		err := visit(member{
			name: zf.Name,
			read: func(budget *int64) ([]byte, error) {
				rc, err := zf.Open()
				if err != nil {
					return nil, sevenzipError(err)
				}
				defer rc.Close()
				body, err := readLimited(rc, budget)
				return body, sevenzipError(err)
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func sevenzipError(err error) error {
	var re *sevenzip.ReadError
	if errors.As(err, &re) && re.Encrypted {
		return fmt.Errorf("%w: %v", ErrEncrypted, err)
	}
	if err != nil && !errors.Is(err, errTooLarge) {
		return fmt.Errorf("read 7z: %w", err)
	}
	return err
}

func readLimited(r io.Reader, budget *int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, *budget+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > *budget {
		return nil, errTooLarge
	}
	*budget -= int64(len(body))
	return body, nil
}

// oleSignature opens a Compound File: an old .doc, or a .docx Word saved under a
// password (the encrypted package sits inside such a container).
var oleSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// isEncryptedOffice tells a password-protected .docx: a Compound File holding an
// "EncryptedPackage" stream (its name is stored in UTF-16).
func isEncryptedOffice(data []byte) bool {
	name := []byte{'E', 0, 'n', 0, 'c', 0, 'r', 0, 'y', 0, 'p', 0, 't', 0, 'e', 0, 'd', 0, 'P', 0}
	return bytes.HasPrefix(data, oleSignature) && bytes.Contains(data, name)
}
