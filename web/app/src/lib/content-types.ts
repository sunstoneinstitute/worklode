// The documents register's option lists (WL-REQ-1798). The server refuses
// any value outside them.

export const DOCUMENT_CATEGORY_OPTIONS = [
  { value: "spec", label: "Spec" },
  { value: "plan", label: "Plan" },
  { value: "meeting-notes", label: "Meeting Notes" },
  { value: "deliverables", label: "Deliverables" },
  { value: "sources", label: "Sources" },
]

export const DOCUMENT_TYPE_OPTIONS = [
  { value: "document", label: "Document" },
  { value: "spreadsheet", label: "Spreadsheet" },
  { value: "pdf", label: "PDF" },
  { value: "slides", label: "Slides" },
  { value: "dataset", label: "Dataset" },
  { value: "link", label: "Link" },
]

export const DOCUMENT_STATUS_OPTIONS = [
  { value: "draft", label: "Draft" },
  { value: "in-review", label: "In Review", colorVar: "--color-accent" },
  { value: "final", label: "Final", colorVar: "--color-accent-success" },
]

// The Documents page's tabs, in order (WL-REQ-1805)
export const DOCUMENT_TABS = [
  { value: "specs", label: "Specs" },
  { value: "plans", label: "Plans" },
  { value: "rules", label: "Rules" },
  { value: "files", label: "Files & links" },
] as const

export type DocumentTab = (typeof DOCUMENT_TABS)[number]["value"]

export function isDocumentTab(value: unknown): value is DocumentTab {
  return DOCUMENT_TABS.some((tab) => tab.value === value)
}
