
The page is reached from the cockpit sidebar's Progress entry. Top to bottom:
the **rally band**, one fixed-height row showing the active rally's id, title,
member count and members landed, or "No active rally" muted; **four counts**,
the group sizes; the **section bar**, every section as a slice in state order
(`built` first, `unplanned` last) with a legend naming state, cell style, count
and one-line meaning, and no percentage; the **groups**, each a heading with
count and meaning then one row per spec; the **rally footer**, a fixed-height
bar at the viewport bottom whose space is always reserved.

A spec row has four columns: reference (the row's only link, to the document
page), title, strip (one cell per section in document order including
subsections, wrapping), next act. Clicking the row anywhere except a link
expands it; expansion state is kept in the browser and survives live updates.
The expanded row shows plans one line each in number order (reference, title,
`requires` list, state pill, a task strip colored landed, active, unstarted
with abandoned omitted, "N landed · M open"; a `no_record` plan shows "no
tasks minted"), a sections table (anchor, heading, state pill, covering plans,
subsections indented), and right-aligned actions. The page shows no
percentage: a section is not a unit of work.

