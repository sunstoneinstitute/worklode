import {
  DashboardIcon,
  DescriptionIcon,
  HistoryIcon,
  SettingsIcon,
  TasksIcon,
} from "@/components/icons"

// Shared between the desktop ProjectToolbar and the mobile menu's "project
// folder" card (Figma's Mobile Components > Menu Mobile > "Open Projects")
// — same nav items, same icons, just laid out differently per surface.
// `to: null` marks a page that doesn't exist yet: shown, but not a link.
export const PROJECT_NAV_ITEMS = [
  { label: "Overview", Icon: DashboardIcon, to: "/projects/$projectId" },
  { label: "Tasks", Icon: TasksIcon, to: "/projects/$projectId/tasks" },
  { label: "Documents", Icon: DescriptionIcon, to: "/projects/$projectId/documents" },
  { label: "Activity", Icon: HistoryIcon, to: null },
] as const

export const PROJECT_NAV_SETTINGS = { label: "Settings", Icon: SettingsIcon, to: null } as const
