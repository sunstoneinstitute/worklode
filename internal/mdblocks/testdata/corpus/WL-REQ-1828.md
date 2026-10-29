
Every tool that acts inside a project takes `project`, a project handle from `project_use`. A bare project key, a handle signed for another user, or an expired handle answers `confirm_project`, and the tool does nothing. A tool that needs a project and was given none answers `project_required` with the project list.

A full ref (`WL-<n>`, `WL-SPEC-<n>`, `WL-REQ-<n>`) carries its project key, so a tool given one acts on that item without a handle. It names one item and confirms nothing for later calls.
