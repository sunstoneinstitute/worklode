package model

// Actor is the wire form of an actor: a human, agent, or service identity
// that can hold leases and be granted tokens.
type Actor struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Admin       bool   `json:"admin"`
}
