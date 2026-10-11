import { addDays, format, isSameDay, parseISO } from "date-fns"

/** "9am", or "1:45pm" when the minutes aren't :00 */
function clock(date: Date) {
  return format(date, date.getMinutes() === 0 ? "haaa" : "h:mmaaa")
}

/**
 * A meeting's time for its card: "Today from 9am – 1:45pm",
 * "Tomorrow from …", or "Sep 26 from …" further out.
 */
export function formatMeetingTime(start: string, end: string, now = new Date()) {
  const from = parseISO(start)
  const day = isSameDay(from, now)
    ? "Today"
    : isSameDay(from, addDays(now, 1))
      ? "Tomorrow"
      : format(from, "MMM d")
  return `${day} from ${clock(from)} – ${clock(parseISO(end))}`
}
