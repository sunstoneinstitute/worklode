// The kinds of story a pitch (and so a project) can be. Ideas of type
// Story Pitch pick one of these; Projects use them as their Type.
export const STORY_TYPE_OPTIONS = [
  { value: "data-story", label: "Data Story" },
  { value: "short-story", label: "Short Story" },
]

// General Idea first: it's the default type in the New Idea form
export const IDEA_TYPE_OPTIONS = [
  { value: "general", label: "General Idea" },
  { value: "story-pitch", label: "Story Pitch" },
]
