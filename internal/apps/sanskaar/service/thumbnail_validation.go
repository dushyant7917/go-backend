package service

import "errors"

// resolveThumbnailFileKey enforces the wallpaper/status invariant that a thumbnail exists if
// and only if the resolved type is "video" (mirrored by a DB CHECK constraint on both tables).
// requestThumbnail is the caller-supplied pointer (nil means the client didn't touch the field);
// existingThumbnail is the row's current value, used as the default on Update (pass nil on
// Create, since there's nothing to default to). When finalType is "image", any thumbnail is
// auto-cleared — except a request that explicitly supplies a non-empty thumbnail together with
// type "image" is a contradiction and returns an error instead of silently dropping it.
func resolveThumbnailFileKey(finalType string, requestThumbnail, existingThumbnail *string) (*string, error) {
	final := existingThumbnail
	if requestThumbnail != nil {
		final = requestThumbnail
	}

	switch finalType {
	case "video":
		if final == nil || *final == "" {
			return nil, errors.New("thumbnail_file_key is required when type is video")
		}
	case "image":
		if requestThumbnail != nil && *requestThumbnail != "" {
			return nil, errors.New("thumbnail_file_key must not be set when type is image")
		}
		final = nil
	}

	return final, nil
}
