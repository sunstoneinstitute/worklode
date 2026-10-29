
The cockpit polls a bounded current-meeting endpoint for the signed-in actor. The indicator opens an activation view that shows only what this actor may see, resolves context hints, and asks for confirmation; confirmation atomically creates or finds the meeting and its draft and opens the editor. The editor shows working versus finished state with explicit `Finish minutes` and `Start correction` actions and shows resources outside the body. From a project context, minutes creation preselects that project. A notes surface uses the same editor for a project note.

Out of scope: creating Calendar events from Worklode, automatic activation, copying or indexing transcript text, generating minutes from transcripts, a general attachment system for non-meeting documents, user-defined kinds or properties, and a personal knowledge workspace.

