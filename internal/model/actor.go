package model

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

// RevokeTokenInput is the request body for RevokeToken (DELETE
// /api/v1/tokens). Token may be either the plaintext or its stored hash.
type RevokeTokenInput struct {
	Token string `json:"token"`
}
