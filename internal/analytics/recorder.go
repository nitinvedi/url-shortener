package analytics

import "context"

// ClickRecorder is the non-blocking boundary called after a redirect has been
// successfully resolved. Implementations should enqueue into a bounded buffer
// and return immediately; aggregation and storage belong to the analytics
// milestone, not to the redirect response path.
type ClickRecorder interface {
	RecordClick(ctx context.Context, urlID string)
}
