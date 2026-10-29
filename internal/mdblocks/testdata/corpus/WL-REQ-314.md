
One place in the client handles every id-taking command: an argument matching `^[0-9]+$` is prefixed with the resolved project's key, anything else passes through untouched. Full ids work from anywhere, with no cross-project warning or refusal. With no project resolved, a bare number is an error: `12 is a task number, not a task id, and no current project is set: pass a full id like WL-12, or set current_project`.

