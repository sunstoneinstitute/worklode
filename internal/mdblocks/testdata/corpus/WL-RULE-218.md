
Not built: no meeting, minutes or note entity, Calendar or Meet integration exists. WL-972 plans it. The design follows.

A `meeting` is one occurrence of a Calendar event or a manually entered event. Recurring events produce one meeting per occurrence. One meeting belongs to one project, may link to one task or decision task, and may have one `minutes` document. A `minutes` document belongs to one meeting. A `note` belongs directly to one project with no meeting. Both are working knowledge in the backbone document store, with no section anchors, acceptance, coverage, reviewer gates, plan task declarations, or task minting.

Identity: for Google Calendar, the durable key is the calendar identity plus the occurrence event ID. For Google Meet, the event's conference data resolves to the stable `spaces/{space}` resource; a Meet code is an alias. When a call starts the meeting also records its `conferenceRecords/{conference}` identity, which routes transcript and smart-notes events without Drive search.

