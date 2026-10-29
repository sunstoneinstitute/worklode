
When a project is open, the left sidebar carries a link back to all projects,
the project header (name, key and the lifecycle mode pill of WL-REQ-337) and local
navigation in this order: Overview, Crew, Milestones, Work, Progress,
Deliverables, Reviews, Decisions, Documents, Activity, Deleted. Progress
appears only when the project has at least one spec. Reviews, Decisions and
Activity are placeholder pages; Deleted is the tombstone review with its
Restore buttons. Below 880 CSS px the sidebar hides and a menu button in the
project header reopens it as a sheet. Global and local navigation stay
visually and semantically distinct.

Every project-scoped page renders the sidebar, including the task detail page
at its canonical URL `/projects/<proj>/<kind>/<n>` (`GET /tasks/{id}`
redirects there), whose subject is no sidebar destination, so its local
navigation marks nothing current. The selected object has one canonical URL.
Browser back and forward and copying that URL work across full-page and narrow
layouts. `/<ref>` redirects a bare task or document reference to its canonical
URL.
