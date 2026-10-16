import { DashboardIcon, DescriptionIcon, GroupIcon, SettingsIcon, TasksIcon } from "@/components/icons"

// Same nav items and icons for the desktop ProjectToolbar and the mobile
// menu's project card. `spa: false` links are plain `<a href>`s that the
// templ cockpit serves until the page is ported (WL-REQ-1801); porting a
// page flips its flag and names its typed route in `to`.
export const PROJECT_NAV_ITEMS = [
  { label: "Overview", Icon: DashboardIcon, path: "", spa: false },
  { label: "Tasks", Icon: TasksIcon, path: "/tasks", spa: true, to: "/projects/$projectId/tasks" },
  { label: "Documents", Icon: DescriptionIcon, path: "/documents", spa: false },
  { label: "Crew", Icon: GroupIcon, path: "/crew", spa: true, to: "/projects/$projectId/crew" },
] as const

export const PROJECT_NAV_SETTINGS = { label: "Settings", Icon: SettingsIcon } as const
