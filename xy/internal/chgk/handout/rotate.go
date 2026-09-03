package handout

import (
	"fmt"
	"image"
	"path"
	"strings"

	corei18n "pecheny.me/dopecore/i18nstrings"
	xystrings "xy/i18nstrings"
	"xy/internal/chgk/imgconv"
)

// The `rotate` block key: a picture that has to be turned a quarter before it
// is laid out. chgksuite writes a rotated temp file and points typst at that;
// this rotates the bytes and hands them over under a name of their own, which
// comes to the same thing without a filesystem.

// ApplyRotation turns every picture a block asks to rotate and returns the
// source with those blocks pointing at the turned copy, plus the pictures it
// added. Nothing is rotated twice: a picture used at two angles gets a name per
// angle.
func ApplyRotation(hndt string, images map[string][]byte) (string, map[string][]byte, error) {
	if !strings.Contains(hndt, "rotate:") {
		return hndt, images, nil
	}
	out := make(map[string][]byte, len(images))
	for k, v := range images {
		out[k] = v
	}
	var rewritten []string
	for _, raw := range splitBlocksKeepingAll(hndt) {
		if raw == "---" {
			rewritten = append(rewritten, raw)
			continue
		}
		name, dir := imageAndRotation(raw)
		if name == "" || dir == "" {
			rewritten = append(rewritten, raw)
			continue
		}
		data, ok := images[name]
		if !ok {
			return "", nil, corei18n.User(xystrings.Default.Docs.Handout.RotateImageMissing(name))
		}
		turned := rotatedName(name, dir)
		if _, done := out[turned]; !done {
			b, err := rotateQuarter(data, dir)
			if err != nil {
				return "", nil, fmt.Errorf("rotate %s: %w", name, err)
			}
			out[turned] = b
		}
		rewritten = append(rewritten, replaceImageLine(raw, turned))
	}
	return strings.Join(rewritten, "\n"), out, nil
}

// splitBlocksKeepingAll splits on the "---" separator lines but keeps them, so
// the source can be put back together unchanged where nothing was rotated.
func splitBlocksKeepingAll(contents string) []string {
	var out []string
	cur := []string{}
	for _, ln := range strings.Split(contents, "\n") {
		if ln == "---" {
			out = append(out, strings.Join(cur, "\n"), "---")
			cur = nil
			continue
		}
		cur = append(cur, ln)
	}
	return append(out, strings.Join(cur, "\n"))
}

func imageAndRotation(raw string) (name, dir string) {
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "image":
			name = strings.TrimSpace(value)
		case "rotate":
			dir = strings.TrimSpace(value)
		}
	}
	if dir != "r" && dir != "l" {
		return name, ""
	}
	return name, dir
}

func replaceImageLine(raw, name string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if key, _, ok := strings.Cut(line, ":"); ok && key == "image" {
			lines[i] = "image: " + name
		}
	}
	return strings.Join(lines, "\n")
}

func rotatedName(name, dir string) string {
	return strings.TrimSuffix(name, path.Ext(name)) + "__rot" + dir + ".png"
}

// rotateQuarter turns the picture a quarter: "r" clockwise, "l" the other way,
// which is what Pillow's rotate(-90) and rotate(90) do.
func rotateQuarter(data []byte, dir string) ([]byte, error) {
	src, err := imgconv.Decode(data)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i, j := x-b.Min.X, y-b.Min.Y
			if dir == "r" {
				dst.Set(b.Dy()-1-j, i, src.At(x, y))
			} else {
				dst.Set(j, b.Dx()-1-i, src.At(x, y))
			}
		}
	}
	// PNG whatever came in: the turned copy is handed to typst, not saved, so
	// it costs nothing to keep it lossless.
	return imgconv.EncodePNG(dst)
}

// flattenImagePaths rewrites every `image:` line to a bare name and rekeys the
// pictures to match. `handouts generate` writes the absolute path chgksuite
// writes, and the typst this runs serves its files from memory under the name
// it is given: a path is not one. Two pictures that share a basename get a
// number, so a .hndt gathering pictures from two folders still renders.
func flattenImagePaths(hndt string, images map[string][]byte) (string, map[string][]byte) {
	if !strings.Contains(hndt, "/") && !strings.Contains(hndt, `\\`) {
		return hndt, images
	}
	flat := map[string]string{}
	taken := map[string]bool{}
	out := map[string][]byte{}
	var rewritten []string
	for _, raw := range splitBlocksKeepingAll(hndt) {
		if raw == "---" {
			rewritten = append(rewritten, raw)
			continue
		}
		name, _ := imageAndRotation(raw)
		if name == "" || name == path.Base(name) {
			rewritten = append(rewritten, raw)
			if data, ok := images[name]; ok && name != "" {
				out[name] = data
			}
			continue
		}
		short, seen := flat[name]
		if !seen {
			short = path.Base(strings.ReplaceAll(name, `\\`, "/"))
			for n := 1; taken[short]; n++ {
				ext := path.Ext(short)
				short = fmt.Sprintf("%s_%d%s", strings.TrimSuffix(path.Base(name), ext), n, ext)
			}
			taken[short] = true
			flat[name] = short
			if data, ok := images[name]; ok {
				out[short] = data
			}
		}
		rewritten = append(rewritten, replaceImageLine(raw, short))
	}
	if len(flat) == 0 {
		return hndt, images
	}
	return strings.Join(rewritten, "\n"), out
}
