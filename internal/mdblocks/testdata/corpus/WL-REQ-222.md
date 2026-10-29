
An event's title and description are scanned for `KEY-<n>` (task), `KEY-TYPE-<n>` (typed entity), and `Worklode project: <key-or-name>`. The grammar `\b([A-Z]+)(-[A-Z]+)?-(\d+)\b` is followed by semantic validation: the key must resolve to a project, a full reference must resolve, and the organizer must be Crew of that project. One valid result preselects its project; none requires a choice; several conflicting results require a choice and never create several links. Confirmation creates the meeting.

