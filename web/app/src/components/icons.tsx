// Custom icon font ("wlicon") built from Material Symbols Outlined SVGs
// (weight 300, filled) via fantasticon — see .fantasticonrc.json. Only the
// glyphs this app actually uses, instead of shipping the full variable
// Material Symbols font.
type IconProps = {
  name: string
  // null = no inline font size, so a text-* class (incl. responsive
  // variants) can size the icon instead
  size?: number | null
  className?: string
}

function Icon({ name, size = 24, className }: IconProps) {
  return (
    <i
      className={`wlicon-${name} leading-none ${className ?? ""}`}
      style={size == null ? undefined : { fontSize: size }}
      aria-hidden="true"
    />
  )
}

// Re-exported under the names of the Figma icon instances they stand in for
// (Figma's own Code Connect mapping wasn't available on this plan's seat, so
// these are matched by hand from the layer names).
export const TasksIcon = (props: Omit<IconProps, "name">) => <Icon name="checklist" {...props} />
export const SwapVerticalIcon = (props: Omit<IconProps, "name">) => <Icon name="swap-vert" {...props} />
export const ArrowRightIcon = (props: Omit<IconProps, "name">) => <Icon name="arrow-forward" {...props} />
export const PencilIcon = (props: Omit<IconProps, "name">) => <Icon name="edit" {...props} />
export const TrashIcon = (props: Omit<IconProps, "name">) => <Icon name="delete" {...props} />
export const SearchIcon = (props: Omit<IconProps, "name">) => <Icon name="search" {...props} />
export const NotificationsIcon = (props: Omit<IconProps, "name">) => <Icon name="notifications" {...props} />
export const DarkModeIcon = (props: Omit<IconProps, "name">) => <Icon name="dark-mode" {...props} />
export const LightModeIcon = (props: Omit<IconProps, "name">) => <Icon name="light-mode" {...props} />
export const MenuIcon = (props: Omit<IconProps, "name">) => <Icon name="menu" {...props} />
export const ScheduleIcon = (props: Omit<IconProps, "name">) => <Icon name="schedule" {...props} />
export const DashboardIcon = (props: Omit<IconProps, "name">) => <Icon name="dashboard" {...props} />
export const DescriptionIcon = (props: Omit<IconProps, "name">) => <Icon name="description" {...props} />
export const HistoryIcon = (props: Omit<IconProps, "name">) => <Icon name="history" {...props} />
export const SettingsIcon = (props: Omit<IconProps, "name">) => <Icon name="settings" {...props} />
export const CloseIcon = (props: Omit<IconProps, "name">) => <Icon name="close" {...props} />
export const ViewColumnIcon = (props: Omit<IconProps, "name">) => <Icon name="view-column" {...props} />
export const DownloadIcon = (props: Omit<IconProps, "name">) => <Icon name="download" {...props} />
export const PreviewIcon = (props: Omit<IconProps, "name">) => <Icon name="preview" {...props} />
export const FilterIcon = (props: Omit<IconProps, "name">) => <Icon name="filter_list" {...props} />
export const CheckIcon = (props: Omit<IconProps, "name">) => <Icon name="check" {...props} />
export const VisibilityIcon = (props: Omit<IconProps, "name">) => <Icon name="visibility" {...props} />
export const VisibilityOffIcon = (props: Omit<IconProps, "name">) => <Icon name="visibility-off" {...props} />
export const DragIndicatorIcon = (props: Omit<IconProps, "name">) => <Icon name="drag-indicator" {...props} />
export const StarIcon = (props: Omit<IconProps, "name">) => <Icon name="star" {...props} />
export const ForwardIcon = (props: Omit<IconProps, "name">) => <Icon name="forward" {...props} />
export const ChevronRightIcon = (props: Omit<IconProps, "name">) => <Icon name="chevron-right" {...props} />
export const FactCheckIcon = (props: Omit<IconProps, "name">) => <Icon name="fact-check" {...props} />
export const AddIcon = (props: Omit<IconProps, "name">) => <Icon name="add" {...props} />
export const ImportantIcon = (props: Omit<IconProps, "name">) => <Icon name="error" {...props} />
export const PendingIcon = (props: Omit<IconProps, "name">) => <Icon name="pending" {...props} />
export const GroupIcon = (props: Omit<IconProps, "name">) => <Icon name="group" {...props} />
