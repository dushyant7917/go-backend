package service

import "testing"

func TestMergeMetadataValue(t *testing.T) {
	tests := []struct {
		name string
		dst  map[string]interface{}
		src  map[string]interface{}
		want map[string]interface{}
	}{
		{
			name: "flat key overwrite",
			dst:  map[string]interface{}{"profile_picture_key": "old.png", "language_code": "en"},
			src:  map[string]interface{}{"profile_picture_key": "new.png"},
			want: map[string]interface{}{"profile_picture_key": "new.png", "language_code": "en"},
		},
		{
			name: "nested map merge preserves sibling keys",
			dst: map[string]interface{}{
				"status_data": map[string]interface{}{
					"picture_key": "status-pictures/old.png",
					"theme":       "dark",
				},
			},
			src: map[string]interface{}{
				"status_data": map[string]interface{}{
					"picture_key": "status-pictures/new.png",
				},
			},
			want: map[string]interface{}{
				"status_data": map[string]interface{}{
					"picture_key": "status-pictures/new.png",
					"theme":       "dark",
				},
			},
		},
		{
			name: "nested key fully replaced when incoming value isn't a map",
			dst: map[string]interface{}{
				"status_data": map[string]interface{}{
					"picture_key": "status-pictures/old.png",
					"theme":       "dark",
				},
			},
			src:  map[string]interface{}{"status_data": "cleared"},
			want: map[string]interface{}{"status_data": "cleared"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeMetadataValue(tt.dst, tt.src)
			if !deepEqual(tt.dst, tt.want) {
				t.Errorf("mergeMetadataValue() = %#v, want %#v", tt.dst, tt.want)
			}
		})
	}
}

// deepEqual is a minimal recursive equality check for map[string]interface{} values, sufficient
// for the string/map shapes used in these tests.
func deepEqual(a, b interface{}) bool {
	aMap, aIsMap := a.(map[string]interface{})
	bMap, bIsMap := b.(map[string]interface{})
	if aIsMap != bIsMap {
		return false
	}
	if aIsMap {
		if len(aMap) != len(bMap) {
			return false
		}
		for k, v := range aMap {
			bv, ok := bMap[k]
			if !ok || !deepEqual(v, bv) {
				return false
			}
		}
		return true
	}
	return a == b
}
