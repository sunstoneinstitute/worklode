package model

import "time"

// ActorKinds is the actors.kind CHECK constraint's value set. Postgres is
// the authority; this is the one Go copy, read by internal/api's gate and the
// CLI's completion. internal/store's TestActorKindCheckConstraintMatchesModel
// reads the constraint back and fails if the two drift.
var ActorKinds = []string{"human", "agent", "service"}

// Actor is the wire form of an actor: a human, agent, or service identity
// that can hold leases and be granted tokens.
type Actor struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

// CreateActorInput is the request body for CreateActor (POST
// /api/v1/actors). Admin grants the actor the right to manage projects,
// actors, and tokens.
type CreateActorInput struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}

// CreateTokenInput is the request body for CreateToken (POST
// /api/v1/actors/{id}/tokens). A nil ExpiresAt means the token never
// expires.
type CreateTokenInput struct {
	Description string  `json:"description"`
	ExpiresAt   *string `json:"expires_at"`
}

// TaskTokenInput is the request body for POST /api/v1/tasks/{id}/tokens
// (001 §2.1, WL-306). Actor names the agent actor the token is attributed
// to, defaulting to "sandbox" (auto-provisioned, kind agent). TTLSeconds
// defaults to the lease TTL; the token also extends with lease renewals and
// is revoked when the lease ends, whatever value is set here.
type TaskTokenInput struct {
	Actor      string `json:"actor,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

// TaskTokenResponse is that endpoint's response: the plaintext (returned
// exactly once), who it acts as, the task it is bound to, and when it
// expires absent renewals.
type TaskTokenResponse struct {
	Token     string    `json:"token"`
	Actor     string    `json:"actor"`
	Task      string    `json:"task"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TokenResponse is the response body of CreateToken: the plaintext token,
// returned exactly once.
type TokenResponse struct {
	Token string `json:"token"`
}

// RevokeTokenInput is the request body for RevokeToken (DELETE
// /api/v1/tokens). Token may be either the plaintext or its stored hash.
type RevokeTokenInput struct {
	Token string `json:"token"`
}
