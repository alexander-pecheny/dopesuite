package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"dope/dope/domain/protocol"
)

// ErrRatingRosterImmutable is returned when a patch tries to mutate a roster
// that is owned by a rating.chgk.info import.
var ErrRatingRosterImmutable = errors.New("команды загружаются из rating.chgk.info; чтобы изменить список, переимпортируйте участников")

// JSONPathSegment is one resolved step of a JSON-patch path: either an object
// key or an array index.
type JSONPathSegment struct {
	Key     string
	Index   int
	IsIndex bool
}

// PatchPathTouchesRatingRoster reports whether a patch path would mutate the
// immutable rating-imported roster the game's Protocol declares.
func PatchPathTouchesRatingRoster(gameType string, path []JSONPathSegment) bool {
	key, ok := protocol.RatingRosterStateKey(gameType)
	return ok && len(path) > 0 && !path[0].IsIndex && path[0].Key == key
}

const maxPatchArrayIndex = 4096

// ParseJSONPatchPath resolves the raw JSON-patch path segments (each a string
// key or non-negative integer index) into typed JSONPathSegments.
func ParseJSONPatchPath(parts []json.RawMessage) ([]JSONPathSegment, error) {
	if len(parts) == 0 {
		return nil, errors.New("empty patch path")
	}
	path := make([]JSONPathSegment, 0, len(parts))
	for _, raw := range parts {
		var key string
		if err := json.Unmarshal(raw, &key); err == nil {
			if key == "" {
				return nil, errors.New("empty patch path key")
			}
			path = append(path, JSONPathSegment{Key: key})
			continue
		}

		var number json.Number
		if err := json.Unmarshal(raw, &number); err != nil {
			return nil, errors.New("patch path segment must be string or non-negative integer")
		}
		index64, err := strconv.ParseInt(number.String(), 10, 0)
		if err != nil || index64 < 0 {
			return nil, errors.New("patch path index must be a non-negative integer")
		}
		if index64 > maxPatchArrayIndex {
			return nil, fmt.Errorf("patch path index exceeds limit (%d)", maxPatchArrayIndex)
		}
		path = append(path, JSONPathSegment{Index: int(index64), IsIndex: true})
	}
	return path, nil
}

// DecodePatchValue decodes a raw patch value into a generic Go value.
func DecodePatchValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing patch value")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// ApplyJSONSet sets value at the given path within root, creating intermediate
// objects/arrays as needed, and returns the new root.
func ApplyJSONSet(root any, path []JSONPathSegment, value any) (any, error) {
	if len(path) == 0 {
		return value, nil
	}

	seg := path[0]
	if seg.IsIndex {
		var arr []any
		switch current := root.(type) {
		case nil:
			arr = []any{}
		case []any:
			arr = current
		default:
			return nil, errors.New("patch path crosses non-array value")
		}
		for len(arr) <= seg.Index {
			arr = append(arr, nil)
		}
		next, err := ApplyJSONSet(arr[seg.Index], path[1:], value)
		if err != nil {
			return nil, err
		}
		arr[seg.Index] = next
		return arr, nil
	}

	var obj map[string]any
	switch current := root.(type) {
	case nil:
		obj = map[string]any{}
	case map[string]any:
		obj = current
	default:
		return nil, errors.New("patch path crosses non-object value")
	}
	next, err := ApplyJSONSet(obj[seg.Key], path[1:], value)
	if err != nil {
		return nil, err
	}
	obj[seg.Key] = next
	return obj, nil
}
