// The ideas list, shared between the Ideas table and the New Idea form.
// A module-level store (not React state) so an idea submitted from the
// full-page form at /ideas/new is still there when the Ideas table mounts
// again — it reads getIdeas() for its starting rows and writes every edit
// back with setIdeas(). Placeholder data until there's a backend.

export type Idea = {
  id: string
  title: string
  author: string
  type: string
  status: string
  submitted: string
  /** Body text from the New Idea form (BlockNote later) */
  body?: string
  /** Story Pitch only: one of STORY_TYPE_OPTIONS (Data Story, Short Story) */
  storyType?: string
  /** Optional, Story Pitch only */
  litmusTestId?: string
  /** Where an idea in intake is in the intake flow (see IdeaIntake) */
  intake?: IntakeProgress
}

export type IntakeDecision = "accept" | "narrow" | "park" | "stop"

export type IntakeProgress = {
  /** "Authorize AI pre-research" was chosen */
  preResearchAuthorized: boolean
  /** The final call, once made */
  decision?: IntakeDecision
}

// The idea pipeline, earliest stage first. There's no "Project" stage: an
// idea that becomes a project leaves Ideas for the Projects list.
export const IDEA_STATUS_OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "intake", label: "Intake", colorVar: "--color-accent" },
]

const SAMPLE_IDEAS: Idea[] = [
  {
    id: "i1",
    title: "Where the glaciers went",
    author: "u2",
    type: "story-pitch",
    storyType: "data-story",
    status: "intake",
    submitted: "2026-09-02",
    litmusTestId: "LT-2026-081",
    // Placeholder pitch text
    body:
      "What happens to a valley when its glacier is gone? Across the Alps, glaciers have lost more than half their volume since 1930, and the pace is accelerating. This story would follow the water: where it went, who depended on it, and what replaces it.\n\n" +
      "It matters for Sunstone because it turns a global number into places people can picture — the farms, hydropower dams and ski towns downstream — and it sits squarely in the Sunstone 30's climate adaptation theme.\n\n" +
      "The data exists: the Swiss Glacier Monitoring Network's century of measurements, satellite ice-extent series, and river discharge records from national hydrology offices. We'd pair maps of retreat with stories from three valleys at different stages of loss.",
  },
  { id: "i2", title: "Reusable map legend", author: "u3", type: "general", status: "draft", submitted: "2026-09-12" },
  { id: "i4", title: "Learn D3 transitions", author: "u4", type: "general", status: "draft", submitted: "2026-09-19" },
  { id: "i5", title: "The ferryman's last season", author: "u2", type: "story-pitch", storyType: "short-story", status: "draft", submitted: "2026-09-21" },
]

let ideas = SAMPLE_IDEAS
let nextId = ideas.length + 1

export function getIdeas() {
  return ideas
}

export function setIdeas(next: Idea[]) {
  ideas = next
}

export function addIdea(idea: Idea) {
  ideas = [...ideas, idea]
}

export function updateIdea(id: string, patch: Partial<Idea>) {
  ideas = ideas.map((idea) => (idea.id === id ? { ...idea, ...patch } : idea))
}

/** Back to the sample ideas — for tests, which share this module */
export function resetIdeas() {
  ideas = SAMPLE_IDEAS
}

export function newIdeaId() {
  return `i${nextId++}`
}
