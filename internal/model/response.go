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

// WebhookAck is what a signed-webhook handler (internal/hooks) answers with:
// "ok" when the delivery was recorded, "duplicate" when its delivery id had
// already been seen, "ignored" for an event this instance does not act on.
// The sender is GitHub or Flux, so this is the only half of a webhook
// exchange worklode declares — the payloads are theirs.
type WebhookAck struct {
	Status string `json:"status"`
}
