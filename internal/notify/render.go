package notify

// renderEvent is the lightweight in-process renderer used by LogNotifier
// (--development mode and unit tests). It is initialized once at
// notifier-construction time. Production code writes pre-rendered
// (subject, body) to the outbox via the renderer in renderer.go; the
// worker later picks it up and never re-renders.
var renderEvent = func(eventKind string, event any, recipientMemberID string) (subject, body string, err error) {
	return defaultRenderer.render(eventKind, event, recipientMemberID)
}

// defaultRenderer is the package-level renderer used by LogNotifier. It
// is initialized in init() with the bundled templates; tests can
// override the package-level var to inject a custom renderer.
//
// Production notifier construction (ChannelNotifier, added in step 8)
// also uses this renderer for the production path. The outbox row
// stores the rendered strings so the worker doesn't need to know about
// templates.
var defaultRenderer = func() *renderer {
	r, err := newRenderer("http://localhost:8080", nil)
	if err != nil {
		// Bundled templates should never fail to parse. If they do,
		// it's a build-time problem and we want to crash early.
		panic(err)
	}
	return r
}()
