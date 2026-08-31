package pptx

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"

	"xy/internal/chgk/imgconv"
)

// optimizeImages is common.optimize_ooxml_images, which --optimize_size runs
// over a finished presentation: every picture is re-encoded, and the smaller of
// the two is kept. A picture with transparency stays a PNG; anything else
// becomes a JPEG, which is where the saving is — a 30 MB deck of photographs
// comes out a fraction of that.
//
// The rules are chgksuite's; the bytes are Go's encoders', not Pillow's, so a
// package built with this on is no longer byte-comparable to chgksuite's. That
// is why the parity oracles are generated with it off.
func (p *pkg) optimizeImages(quality int) {
	renamed := map[string]string{}
	newTypes := map[string]string{}
	// Every name the package started with stays reserved, so a picture that
	// changes format never takes a name another one had.
	reserved := map[string]bool{}
	for name := range p.parts {
		reserved[name] = true
	}

	for _, name := range p.imagePartNames() {
		data := p.parts[name]
		ext := strings.ToLower(strings.TrimPrefix(pathExt(name), "."))
		smaller, newExt, ok := optimizeImage(data, ext, quality)
		if !ok {
			continue
		}
		target := name
		if !sameImageExt(newExt, ext) {
			target = p.nextMediaName(reserved, name, newExt)
			reserved[target] = true
			renamed[name] = target
			p.drop(name)
		}
		p.put(target, smaller)
		newTypes[newExt] = imageContentTypes[newExt]
	}
	if len(renamed) == 0 && len(newTypes) == 0 {
		return
	}
	p.retargetImageRels(renamed)
}

// optimizeImage is optimize_raster_image_data: the candidates, and the smallest
// that actually beats what was there.
func optimizeImage(data []byte, ext string, quality int) ([]byte, string, bool) {
	img, err := imgconv.Decode(data)
	if err != nil {
		return nil, "", false
	}
	type candidate struct {
		ext  string
		data []byte
	}
	var candidates []candidate
	if imgconv.HasAlpha(img) {
		if data, err := encodePNG(img); err == nil {
			candidates = append(candidates, candidate{"png", data})
		}
	} else {
		if data, err := encodeJPEG(img, quality); err == nil {
			candidates = append(candidates, candidate{"jpg", data})
		}
		if ext == "png" {
			if data, err := encodePNG(img); err == nil {
				candidates = append(candidates, candidate{"png", data})
			}
		}
	}
	best := candidate{}
	for _, c := range candidates {
		if len(c.data) >= len(data) {
			continue
		}
		if best.data == nil || len(c.data) < len(best.data) {
			best = c
		}
	}
	if best.data == nil {
		return nil, "", false
	}
	return best.data, best.ext, true
}

// nextMediaName is _next_ooxml_media_part_name: the same stem with the new
// extension, and failing that the stem with a number after it.
func (p *pkg) nextMediaName(reserved map[string]bool, original, ext string) string {
	stem := strings.TrimSuffix(original, pathExt(original))
	if candidate := stem + "." + ext; !reserved[candidate] {
		return candidate
	}
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s_%d.%s", stem, n, ext)
		if !reserved[candidate] {
			return candidate
		}
	}
}

// encodeJPEG is Pillow's `save(quality=…, optimize=True)`: Go's encoder writes
// the fixed Huffman tables, and imgconv.OptimizeJPEG replaces them with the
// ones this picture earns. Without that second pass a deck comes out a tenth
// larger than chgksuite's; with it, the two are within a rounding error.
func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	optimized, err := imgconv.OptimizeJPEG(buf.Bytes())
	if err != nil {
		// A file it will not rewrite is still a good file.
		return buf.Bytes(), nil //nolint:nilerr // the unoptimized bytes are the fallback
	}
	return optimized, nil
}

// encodePNG is Pillow's `save(optimize=True, compress_level=9)`.
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sameImageExt(a, b string) bool {
	if a == b {
		return true
	}
	return (a == "jpg" && b == "jpeg") || (a == "jpeg" && b == "jpg")
}

func (p *pkg) imagePartNames() []string {
	var names []string
	for _, name := range p.order {
		if strings.HasPrefix(name, "ppt/media/") {
			names = append(names, name)
		}
	}
	return names
}

func pathExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}

var reImageRel = regexp.MustCompile(`(<Relationship Id="rId\d+" Type="[^"]*/image" Target=")([^"]+)(")`)

// retargetImageRels points every relationship at the picture's new name. A slide
// the export built holds its own list; a template part is patched in place.
func (p *pkg) retargetImageRels(renamed map[string]string) {
	if len(renamed) == 0 {
		return
	}
	for _, s := range p.slides {
		for i, r := range s.rels {
			if r.relType != relImage {
				continue
			}
			if to, ok := renamed["ppt/media/"+strings.TrimPrefix(r.target, "../media/")]; ok {
				s.rels[i].target = "../media/" + strings.TrimPrefix(to, "ppt/media/")
			}
		}
	}
	for _, name := range p.order {
		i := strings.Index(name, "_rels/")
		if !strings.HasSuffix(name, ".rels") || i < 0 {
			continue
		}
		dir := strings.TrimSuffix(name[:i], "/")
		patched := reImageRel.ReplaceAllStringFunc(string(p.parts[name]), func(s string) string {
			m := reImageRel.FindStringSubmatch(s)
			from := resolveTarget(dir, m[2])
			to, ok := renamed[from]
			if !ok {
				return s
			}
			return m[1] + relativeTarget(dir, to) + m[3]
		})
		p.parts[name] = []byte(patched)
	}
}

// resolveTarget turns a relationship's target into the part name it means.
func resolveTarget(dir, target string) string {
	target = strings.TrimPrefix(target, "/")
	for strings.HasPrefix(target, "../") {
		target = target[3:]
		if i := strings.LastIndexByte(dir, '/'); i >= 0 {
			dir = dir[:i]
		}
	}
	if dir == "" {
		return target
	}
	return dir + "/" + target
}

func relativeTarget(dir, part string) string {
	if dir == "" {
		return part
	}
	depth := strings.Count(dir, "/") + 1
	rest := part
	for range depth {
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[i+1:]
		}
	}
	return strings.Repeat("../", depth-1) + rest
}
