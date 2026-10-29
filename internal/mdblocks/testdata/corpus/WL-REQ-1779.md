
When it opens one, the worker sets `task_reviewers` to the human who created the
task, or to the project owner when an agent created it, and writes one line on
the task saying why a human should look. It does not arm auto-merge on that
change: the change merges after an `approved` verdict (WL-REQ-1324). The review task's
body names the task ref and links each pull request on the task, so the
reviewer reaches the change in one click.

