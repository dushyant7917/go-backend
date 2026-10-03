// Package r2cleanup centralizes two R2 file-lifecycle checks shared by any app that lets a
// client submit a file_key it uploaded via a presigned URL: verifying the key actually exists
// in storage before a create/update accepts it, and best-effort deleting old keys an update just
// replaced. Extracted from Sanskaar's service layer so a future app can reuse it directly instead
// of re-copying the same two helpers.
package r2cleanup

import (
	"fmt"

	r2ConfigService "go-backend/internal/apps/r2/config/service"

	"github.com/getsentry/sentry-go"
)

// VerifyFilesExist checks that every given (non-empty) file key exists in R2 under appName's
// client, returning a descriptive error for the first one that doesn't. Call this before
// Create/Update accepts a client-supplied file_key, so a request can't reference a file that was
// never actually uploaded (e.g. the client skipped or failed the presigned-URL PUT step) —
// without this, the DB would end up pointing at a dead key, and a later cleanup could then delete
// the only remaining copy of the file it was meant to replace.
func VerifyFilesExist(factory *r2ConfigService.R2ClientFactory, appName, bucketName string, fileKeys ...string) error {
	r2Client, clientErr := factory.GetClient(appName)

	for _, key := range fileKeys {
		if key == "" {
			continue
		}
		if clientErr != nil {
			return fmt.Errorf("failed to get R2 client to verify file %q: %w", key, clientErr)
		}
		exists, err := r2Client.FileExists(bucketName, key)
		if err != nil {
			return fmt.Errorf("failed to verify file %q in storage: %w", key, err)
		}
		if !exists {
			return fmt.Errorf("file %q does not exist in storage; request an upload URL and upload the file before referencing it", key)
		}
	}

	return nil
}

// DeleteOldFiles best-effort deletes R2 objects that an update just replaced (e.g. a new
// audio_file_key/thumbnail_file_key/media_file_key uploaded to replace an old one). It never
// returns an error: by the time this runs, the DB update has already succeeded, so a delete
// failure here is an orphaned file, not a request failure — it's reported to Sentry instead of
// failing the response.
func DeleteOldFiles(factory *r2ConfigService.R2ClientFactory, appName, bucketName string, oldKeys ...string) {
	var toDelete []string
	for _, key := range oldKeys {
		if key != "" {
			toDelete = append(toDelete, key)
		}
	}
	if len(toDelete) == 0 {
		return
	}

	r2Client, err := factory.GetClient(appName)
	if err != nil {
		sentry.CaptureException(fmt.Errorf("%s: failed to get R2 client to delete old files %v: %w", appName, toDelete, err))
		return
	}

	for _, key := range toDelete {
		if err := r2Client.DeleteFile(bucketName, key); err != nil {
			sentry.CaptureException(fmt.Errorf("%s: failed to delete old R2 file %q: %w", appName, key, err))
		}
	}
}
