package hooks

// ParseRevisionForTest exposes parseRevision to the external test package.
var ParseRevisionForTest = parseRevision

// NewGitHubHandlerWithResolver exposes the common githubHandler constructor
// to the external test package, so a test can stub resolveBranch directly
// instead of standing up a fake GitHub App server.
var NewGitHubHandlerWithResolver = newGitHubHandler
