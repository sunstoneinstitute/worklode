package api

// SignSessionForTest exposes signSession to the api_test package, which needs
// a valid session cookie to exercise the blob route's web-session path.
var SignSessionForTest = signSession
