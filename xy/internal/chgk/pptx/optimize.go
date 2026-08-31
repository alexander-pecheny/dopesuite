package pptx

import (
	"fmt"
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
		smaller, newExt, ok := imgconv.Optimize(data, ext, quality)
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
