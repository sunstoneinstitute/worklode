
Hierarchy never consults a workflow. A task with children moves only among core states (WL-REQ-143), and `containerForbiddenStates` is every non-core state (`in_review` plus the delivery tail), because those states are earned by facts about a commit a container does not have.

