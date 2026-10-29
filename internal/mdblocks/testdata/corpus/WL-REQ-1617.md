
A watcher rule (`review-on-workflow-change`, peer of `doc-lifecycle`) reads the `project.workflows_set` event of WL-REQ-151 and mints a `review` task, "Review workflow change on <project>", naming the changed workflows and the automations added, removed or changed. It is suppressed while an open review task from this rule exists for the project. The review does not block the change.

Authorship of a workflow is not admin-gated because this review task is the control.

