// Two-letter avatar initials: first + last name initials for multi-part
// names ("Tobias Smith" → "TS"), the first two letters for a single name
// ("Claudicia" → "CL"). One letter alone collides too often across a crew.
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return ""
  const letters =
    parts.length === 1
      ? Array.from(parts[0]).slice(0, 2)
      : [Array.from(parts[0])[0], Array.from(parts.at(-1)!)[0]]
  return letters.join("").toUpperCase()
}
