package model

// ErrorResponse is the body every non-2xx API response carries: the server's
// own message for the failure. internal/cli reads the Error field back out to
// turn a status code into a sentence a human asked for.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse is the body of GET /healthz.
type HealthResponse struct {
	Status string `json:"status"`
}
