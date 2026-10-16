import { differenceInCalendarDays, parseISO } from "date-fns"

function count(amount: number, unit: string) {
  return `${amount} ${unit}${amount === 1 ? "" : "s"}`
}

/**
 * How far an ISO due date is from today, as the summary card shows it:
 * "2 days late", "Due today", "5 days left", then whole weeks from seven
 * days on ("3 weeks left"). Counts calendar days, not 24-hour periods, so
 * the answer doesn't depend on the time of day or on daylight saving.
 * No due date (a document awaiting review) is "".
 */
export function dueLabel(due: string, today: Date = new Date()) {
  if (!due) return ""
  const days = differenceInCalendarDays(parseISO(due), today)
  if (days < 0) return `${count(-days, "day")} late`
  if (days === 0) return "Due today"
  if (days < 7) return `${count(days, "day")} left`
  return `${count(Math.floor(days / 7), "week")} left`
}
