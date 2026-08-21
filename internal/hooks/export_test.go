package hooks

// ParseRevisionForTest exposes parseRevision to the external test package.
var ParseRevisionForTest = parseRevision

// MaxReplayErrorsForTest exposes the reported-error cap, so the test that
// checks the overflow count stays true if the cap moves.
const MaxReplayErrorsForTest = maxReplayErrors

// NewGitHubHandlerWithResolver exposes the common githubHandler constructor
// to the external test package, so a test can stub resolveBranch directly
// instead of standing up a fake GitHub App server.
var NewGitHubHandlerWithResolver = newGitHubHandler
