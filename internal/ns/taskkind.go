package ns

// TaskKindsMatching returns the known kinds whose descriptor satisfies keep,
// in the same order as TaskKinds.
func TaskKindsMatching(keep func(TaskKindDescriptor) bool) []string {
	kinds := make([]string, 0, len(TaskKinds))
	for _, kind := range TaskKinds {
		if descriptor, ok := TaskKindDescriptors[kind]; ok && keep(descriptor) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}
