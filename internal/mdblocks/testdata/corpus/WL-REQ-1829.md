
`confirm_project`, `project_required` and `project_resolve` carry the candidates (key and full name) and this instruction:

"Confirm the project with the user before calling project_use. In a text or graphical interface, ask with your question tool (e.g. AskUserQuestion) offering these projects as options. In a voice conversation, read back the full project name and wait for the user to say yes."

The answer also names the `from_instructions` shortcut. The tool descriptions say the same: use `Worklode project: <name>` from the project instructions with `from_instructions=true`, otherwise confirm with the user first.

