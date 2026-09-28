package store

import (
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// taskKindPolicy refuses unknown kinds before consulting their permissions.
func taskKindPolicy(kind string) (ns.TaskKindDescriptor, error) {
	policy, ok := ns.TaskKindDescriptors[kind]
	if !ok {
		return policy, fmt.Errorf("unknown kind %q: %w", kind, ErrInvalidInput)
	}
	return policy, nil
}
